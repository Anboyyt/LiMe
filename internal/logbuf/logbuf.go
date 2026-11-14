// Package logbuf 在内存里保留最近若干条 slog 记录，供运维面板查看。
// 日志同时照常写到 stdout，这里只做旁路缓存。
package logbuf

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry 是面板展示用的一条日志。
type Entry struct {
	Time    time.Time         `json:"Time"`
	Level   string            `json:"Level"`
	Message string            `json:"Message"`
	Attrs   map[string]string `json:"Attrs,omitempty"`
}

// Buffer 是固定容量的环形日志缓存。
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

var defaultBuffer = &Buffer{max: 1000}

// Default 返回进程级共享缓存。
func Default() *Buffer { return defaultBuffer }

// Append 追加一条日志，超过容量时丢弃最旧的。
func (b *Buffer) Append(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = 1000
	}
	b.entries = append(b.entries, e)
	if len(b.entries) > b.max {
		copy(b.entries, b.entries[len(b.entries)-b.max:])
		b.entries = b.entries[:b.max]
	}
}

// Snapshot 返回从新到旧的日志。level 为空表示全部；否则按最低级别过滤。
func (b *Buffer) Snapshot(limit int, level string) []Entry {
	if limit <= 0 {
		limit = 200
	}
	minLevel := parseLevel(level)

	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, 0, minInt(limit, len(b.entries)))
	for i := len(b.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := b.entries[i]
		if minLevel != nil && levelValue(e.Level) < *minLevel {
			continue
		}
		out = append(out, e)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseLevel(s string) *int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		v := -4
		return &v
	case "INFO":
		v := 0
		return &v
	case "WARN", "WARNING":
		v := 4
		return &v
	case "ERROR":
		v := 8
		return &v
	default:
		return nil
	}
}

func levelValue(s string) int {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return -4
	case "INFO":
		return 0
	case "WARN", "WARNING":
		return 4
	case "ERROR":
		return 8
	default:
		return 0
	}
}

// Handler 包装一个 slog.Handler，把记录同时写进 Buffer。
type Handler struct {
	next  slog.Handler
	buf   *Buffer
	attrs []slog.Attr
}

// NewHandler 创建旁路日志 handler。
func NewHandler(next slog.Handler, buf *Buffer) slog.Handler {
	if buf == nil {
		buf = Default()
	}
	return &Handler{next: next, buf: buf}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	e := Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   make(map[string]string, len(h.attrs)+r.NumAttrs()),
	}
	for _, a := range h.attrs {
		addAttr(e.Attrs, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(e.Attrs, a)
		return true
	})
	h.buf.Append(e)
	return h.next.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := h.next.WithAttrs(attrs)
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &Handler{next: next, buf: h.buf, attrs: merged}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{next: h.next.WithGroup(name), buf: h.buf, attrs: h.attrs}
}

func addAttr(dst map[string]string, a slog.Attr) {
	if a.Key == "" {
		return
	}
	dst[a.Key] = fmt.Sprint(a.Value.Any())
}
