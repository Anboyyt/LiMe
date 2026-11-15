// Package notify 负责把服务端事件推到外部渠道。
// 目前只有 Telegram：播放开始/暂停/继续/停止。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Events 是支持的通知事件名。空字符串表示全部开启。
var Events = []string{"start", "pause", "resume", "stop", "stall", "health"}

// Config 是 Telegram 通知的运行时配置。
type Config struct {
	Token   string
	ChatID  string
	Proxy   string
	Enabled bool
	// Events 为 nil 时表示全部事件；显式传空 map 表示一个都不推送。
	Events map[string]bool
}

// ParseEvents 解析 "start,pause,resume,stop"。
// 空字符串或 all 视为全开；none 视为全关。
func ParseEvents(raw string) map[string]bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	out := make(map[string]bool, len(Events))
	if raw == "" || raw == "all" {
		for _, e := range Events {
			out[e] = true
		}
		return out
	}
	if raw == "none" {
		return out
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, e := range Events {
			if part == e {
				out[e] = true
				break
			}
		}
	}
	return out
}

// EventsString 把事件集合转成稳定的逗号分隔串；全开返回空字符串。
func EventsString(events map[string]bool) string {
	if events == nil {
		return ""
	}
	on := make([]string, 0, len(Events))
	for _, e := range Events {
		if events[e] {
			on = append(on, e)
		}
	}
	if len(on) == len(Events) {
		return ""
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, ",")
}

// Telegram 是带发送队列的 Telegram Bot 客户端。
// 发送失败只记日志，不影响播放接口。
// Message 是一条待发送的通知。Photo 非空时走 sendPhoto（图片 + 说明文字）。
type Message struct {
	Text      string
	Photo     []byte
	PhotoName string
}

type Telegram struct {
	mu      sync.RWMutex
	cfg     Config
	client  *http.Client
	queue   chan Message
	apiBase string
	log     *slog.Logger
}

// New 创建客户端并启动发送 worker。
func New(cfg Config, log *slog.Logger) *Telegram {
	if log == nil {
		log = slog.Default()
	}
	t := &Telegram{
		queue:   make(chan Message, 128),
		apiBase: "https://api.telegram.org",
		log:     log,
	}
	t.SetConfig(cfg)
	go t.run()
	return t
}

// SetConfig 运行时更新配置（面板保存后立即生效）。
func (t *Telegram) SetConfig(cfg Config) {
	t.mu.Lock()
	oldProxy := t.cfg.Proxy
	t.cfg = cfg
	if t.client == nil || oldProxy != cfg.Proxy {
		t.client = newClient(cfg.Proxy)
	}
	t.mu.Unlock()
}

func newClient(proxy string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}
}

// Configured 表示 token 和 chat id 都已填写。
func (t *Telegram) Configured() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cfg.Token != "" && t.cfg.ChatID != ""
}

// Enabled 表示总开关打开且配置完整。
func (t *Telegram) Enabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cfg.Enabled && t.cfg.Token != "" && t.cfg.ChatID != ""
}

// EventEnabled 判断某个事件是否开启推送。
func (t *Telegram) EventEnabled(event string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.cfg.Enabled || t.cfg.Token == "" || t.cfg.ChatID == "" {
		return false
	}
	if t.cfg.Events == nil {
		return true
	}
	return t.cfg.Events[event]
}

// Notify 异步入队一条 HTML 消息。队列满时直接丢弃，避免阻塞播放上报。
func (t *Telegram) Notify(event, text string) {
	t.NotifyMessage(event, Message{Text: text})
}

// NotifyPhoto 异步入队一条带图片的通知；图片为空时退回纯文本。
func (t *Telegram) NotifyPhoto(event, text string, photo []byte, name string) {
	t.NotifyMessage(event, Message{Text: text, Photo: photo, PhotoName: name})
}

func (t *Telegram) NotifyMessage(event string, m Message) {
	if !t.EventEnabled(event) {
		return
	}
	select {
	case t.queue <- m:
	default:
		t.log.Warn("telegram queue full, drop message", "event", event)
	}
}

// SendNow 同步发送，用于面板的「发送测试消息」。
func (t *Telegram) SendNow(ctx context.Context, text string) error {
	if !t.Configured() {
		return errors.New("telegram 未配置 Bot Token 或 Chat ID")
	}
	return t.send(ctx, text)
}

func (t *Telegram) run() {
	for m := range t.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var err error
		if len(m.Photo) > 0 {
			err = t.sendPhoto(ctx, m.Text, m.Photo, m.PhotoName)
			if err != nil {
				// 图片失败不能把通知本身丢掉，退回纯文本再发一次。
				t.log.Warn("telegram photo failed, fallback to text", "err", err)
				err = t.send(ctx, m.Text)
			}
		} else {
			err = t.send(ctx, m.Text)
		}
		cancel()
		if err != nil {
			t.log.Warn("telegram send failed", "err", err)
		}
		// 简单限速，避免连点暂停/播放时刷屏。
		time.Sleep(400 * time.Millisecond)
	}
}

func (t *Telegram) send(ctx context.Context, text string) error {
	t.mu.RLock()
	cfg := t.cfg
	client := t.client
	base := t.apiBase
	t.mu.RUnlock()

	if cfg.Token == "" || cfg.ChatID == "" {
		return errors.New("telegram 未配置")
	}

	payload, err := json.Marshal(map[string]any{
		"chat_id":                  cfg.ChatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}

	// 注意：URL 里带 token，任何错误都不能把完整 URL 带进日志。
	endpoint := base + "/bot" + cfg.Token + "/sendMessage"
	return t.post(ctx, client, endpoint, "application/json", payload)
}

// sendPhoto 用 multipart 上传图片并附带说明文字。Telegram 的 caption
// 上限 1024 字，播放通知远低于这个长度。
func (t *Telegram) sendPhoto(ctx context.Context, caption string, photo []byte, name string) error {
	t.mu.RLock()
	cfg := t.cfg
	client := t.client
	base := t.apiBase
	t.mu.RUnlock()

	if cfg.Token == "" || cfg.ChatID == "" {
		return errors.New("telegram 未配置")
	}
	if name == "" {
		name = "cover.jpg"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", cfg.ChatID)
	_ = w.WriteField("caption", caption)
	_ = w.WriteField("parse_mode", "HTML")
	part, err := w.CreateFormFile("photo", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(photo); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	endpoint := base + "/bot" + cfg.Token + "/sendPhoto"
	return t.post(ctx, client, endpoint, w.FormDataContentType(), buf.Bytes())
}

func (t *Telegram) post(ctx context.Context, client *http.Client, endpoint, contentType string, payload []byte) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("请求 Telegram 失败: %w", err)
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("telegram http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			// 429/5xx 才重试一次。
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return lastErr
			}
		}

		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(2 * time.Second):
		}
	}
	return lastErr
}
