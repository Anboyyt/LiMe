package api

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"lime/internal/store"
)

const (
	defaultSessionInterval = 1500 * time.Millisecond
	minSessionInterval     = 1000 * time.Millisecond
	maxSessionInterval     = 30 * time.Second
)

// wsMessage 是 Jellyfin WebSocket 消息的外壳。字段名必须保持 PascalCase，
// 客户端 SDK 直接按 MessageType 分发。
type wsMessage struct {
	MessageType string `json:"MessageType"`
	MessageId   string `json:"MessageId,omitempty"`
	Data        any    `json:"Data,omitempty"`
}

// wsClient 是一条已升级的 WebSocket 连接。
type wsClient struct {
	conn net.Conn
	user *store.User // 未带有效 token 时为 nil，只能保活、不能订阅

	writeMu sync.Mutex // 控制帧和数据帧共用一条连接，写必须串行
	done    chan struct{}
	once    sync.Once

	// broadcast 用非阻塞投递唤醒订阅协程，避免播放上报被慢客户端拖住。
	notify chan struct{}

	subMu   sync.Mutex
	stopSub chan struct{}
}

func newWSClient(conn net.Conn, user *store.User) *wsClient {
	return &wsClient{
		conn:   conn,
		user:   user,
		done:   make(chan struct{}),
		notify: make(chan struct{}, 1),
	}
}

func (c *wsClient) write(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return writeWSFrame(c.conn, opcode, payload)
}

func (c *wsClient) writeJSON(msg wsMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.write(0x1, raw)
}

func (c *wsClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// subscribe 启动 Sessions 快照推送。重复调用不会叠加协程。
func (c *wsClient) subscribe(h *Handler, interval time.Duration) {
	c.subMu.Lock()
	if c.stopSub != nil {
		c.subMu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.stopSub = stop
	c.subMu.Unlock()

	go h.pushSessions(c, interval, stop)
}

func (c *wsClient) unsubscribe() {
	c.subMu.Lock()
	stop := c.stopSub
	c.stopSub = nil
	c.subMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// clearSub 只清掉自己那次订阅，避免和后来的 SessionsStart 抢状态。
func (c *wsClient) clearSub(stop chan struct{}) {
	c.subMu.Lock()
	if c.stopSub == stop {
		c.stopSub = nil
	}
	c.subMu.Unlock()
}

// sessionHub 保存所有活着的 WebSocket 连接。
type sessionHub struct {
	mu    sync.RWMutex
	conns map[*wsClient]struct{}
}

func newSessionHub() *sessionHub {
	return &sessionHub{conns: make(map[*wsClient]struct{})}
}

func (hub *sessionHub) add(c *wsClient) {
	hub.mu.Lock()
	hub.conns[c] = struct{}{}
	hub.mu.Unlock()
}

func (hub *sessionHub) remove(c *wsClient) {
	hub.mu.Lock()
	delete(hub.conns, c)
	hub.mu.Unlock()
}

// broadcast 让所有订阅者立刻推一次快照。播放开始、进度、暂停、停止后都调用。
func (hub *sessionHub) broadcast() {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for c := range hub.conns {
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

// broadcastMessage 向已认证连接主动推一条消息（Emby 风格的 PlaybackStart /
// PlaybackStopped 走这里，不要求客户端先订阅 Sessions）。
// 非管理员只收自己的会话，避免多用户间互相看到播放内容。
func (hub *sessionHub) broadcastMessage(msg wsMessage, ownerID string) {
	hub.mu.RLock()
	targets := make([]*wsClient, 0, len(hub.conns))
	for c := range hub.conns {
		if c.user == nil {
			continue
		}
		if ownerID != "" && !c.user.IsAdmin && c.user.ID != ownerID {
			continue
		}
		targets = append(targets, c)
	}
	hub.mu.RUnlock()

	for _, c := range targets {
		go func(c *wsClient) {
			if err := c.writeJSON(msg); err != nil {
				c.close()
			}
		}(c)
	}
}

// pushSessions 是单个订阅者的推送循环：订阅时先推一次，之后按客户端给的间隔
// 周期推，同时接受 broadcast 的即时唤醒。
func (h *Handler) pushSessions(c *wsClient, interval time.Duration, stop chan struct{}) {
	defer c.clearSub(stop)

	if interval < minSessionInterval {
		interval = minSessionInterval
	}
	if interval > maxSessionInterval {
		interval = maxSessionInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	h.pushSessionSnapshot(c)
	for {
		select {
		case <-stop:
			return
		case <-c.done:
			return
		case <-c.notify:
		case <-ticker.C:
		}
		h.pushSessionSnapshot(c)
	}
}

func (h *Handler) pushSessionSnapshot(c *wsClient) {
	if c.user == nil {
		return
	}
	sessions, err := h.sessionSnapshot(c.user)
	if err != nil {
		h.log.Warn("build session snapshot", "err", err)
		return
	}
	if err := c.writeJSON(wsMessage{
		MessageType: "Sessions",
		MessageId:   store.MustRandomHex(16),
		Data:        sessions,
	}); err != nil {
		// 写失败通常是客户端已经断了，直接收尾让读循环也退出。
		c.close()
	}
}

func (h *Handler) handleWSMessage(c *wsClient, payload []byte) {
	var msg struct {
		MessageType string          `json:"MessageType"`
		Data        json.RawMessage `json:"Data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}

	switch strings.ToLower(msg.MessageType) {
	case "sessionsstart":
		if c.user == nil {
			h.log.Debug("websocket sessions subscribe without auth")
			return
		}
		h.log.Debug("websocket sessions subscribe", "user", c.user.Name)
		c.subscribe(h, parseSessionInterval(msg.Data))
	case "sessionsstop":
		c.unsubscribe()
	}
}

// parseSessionInterval 解析 Jellyfin 的 SessionsStart 参数："起始延迟,间隔毫秒"。
func parseSessionInterval(raw json.RawMessage) time.Duration {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return defaultSessionInterval
	}
	parts := strings.Split(s, ",")
	if len(parts) < 2 {
		return defaultSessionInterval
	}
	ms, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || ms <= 0 {
		return defaultSessionInterval
	}
	return time.Duration(ms) * time.Millisecond
}
