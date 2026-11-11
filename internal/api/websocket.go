package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"lime/internal/store"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// socket 是 Jellyfin 客户端用的 WebSocket 端点。完成握手后支持
// SessionsStart/SessionsStop 订阅，播放状态变化时推送 Sessions 快照。
func (h *Handler) socket(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || !headerContains(r.Header, "Connection", "upgrade") {
		writeError(w, http.StatusBadRequest, "expected websocket upgrade")
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "hijack unsupported")
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"); err != nil {
		return
	}
	if err := rw.Flush(); err != nil {
		return
	}

	// 订阅会话需要知道是谁，未认证的连接仍然允许保活。
	var user *store.User
	if a := parseAuth(r); a.Token != "" {
		if u, err := h.db.UserByToken(a.Token); err == nil {
			user = u
		}
	}
	client := newWSClient(conn, user)
	h.sessions.add(client)
	defer func() {
		h.sessions.remove(client)
		client.unsubscribe()
		client.close()
	}()

	name := ""
	if user != nil {
		name = user.Name
	}
	h.log.Debug("websocket connected", "remote", r.RemoteAddr, "user", name)
	h.wsLoop(client, rw.Reader)
	h.log.Debug("websocket closed", "remote", r.RemoteAddr, "user", name)
}

func headerContains(h http.Header, name, value string) bool {
	for _, v := range h.Values(name) {
		if strings.Contains(strings.ToLower(v), strings.ToLower(value)) {
			return true
		}
	}
	return false
}

// wsLoop 处理控制帧和文本帧：ping 回 pong，close 关闭，
// 文本帧交给 handleWSMessage 解析 SessionsStart/SessionsStop。
func (h *Handler) wsLoop(c *wsClient, r *bufio.Reader) {
	for {
		// 客户端每 30 秒左右会发一次 keepalive，90 秒没动静就断开
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		opcode, payload, err := readWSFrame(r)
		if err != nil {
			return
		}
		switch opcode {
		case 0x8: // close
			_ = c.write(0x8, nil)
			return
		case 0x9: // ping
			if err := c.write(0xA, payload); err != nil {
				return
			}
		case 0xA: // pong
		case 0x1: // 文本消息
			h.handleWSMessage(c, payload)
		default:
			// 二进制消息忽略
		}
	}
}

func readWSFrame(r *bufio.Reader) (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode := hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	n := int64(hdr[1] & 0x7f)

	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if n < 0 || n > 1<<20 {
		return 0, nil, errors.New("frame too large")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func writeWSFrame(w io.Writer, opcode byte, payload []byte) error {
	hdr := make([]byte, 0, 10)
	hdr = append(hdr, 0x80|opcode)
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 65535:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, ext[:]...)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if n > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}
