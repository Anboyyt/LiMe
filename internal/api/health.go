package api

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"runtime"
	"sync"
	"time"

	"lime/internal/version"
)

type healthMetrics struct {
	DiskFreeBytes  uint64
	DiskTotalBytes uint64
	DBLatencyMs    int64
	Goroutines     int
}

func (h *Handler) healthMetrics() healthMetrics {
	m := healthMetrics{Goroutines: runtime.NumGoroutine()}
	if free, total, err := diskUsage(h.cfg.DataDir); err == nil {
		m.DiskFreeBytes, m.DiskTotalBytes = free, total
	}
	start := time.Now()
	var one int
	if err := h.db.QueryRow(`SELECT 1`).Scan(&one); err == nil {
		m.DBLatencyMs = time.Since(start).Milliseconds()
	}
	return m
}

// memCache 缓存 5 秒内的内存快照。ReadMemStats 会短暂 STW，
// healthcheck 每 30 秒一次没必要每次都读。
var memCache struct {
	sync.Mutex
	at         time.Time
	alloc, sys uint64
}

func cachedMem() (alloc, sys uint64) {
	memCache.Lock()
	defer memCache.Unlock()
	if time.Since(memCache.at) < 5*time.Second {
		return memCache.alloc, memCache.sys
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memCache.alloc, memCache.sys, memCache.at = m.Alloc, m.Sys, time.Now()
	return memCache.alloc, memCache.sys
}

// deepHealth 给容器 healthcheck 和排查用：确认数据库能应答，同时带一份
// 运行时快照。整体 2 秒上限，数据库卡死时直接判 degraded，不会把容器挂着。
func (h *Handler) deepHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	start := time.Now()
	dbOK := true
	var one int
	if err := h.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		dbOK = false
	}
	latency := time.Since(start).Milliseconds()

	alloc, sys := cachedMem()
	status := "ok"
	code := http.StatusOK
	if !dbOK || latency > 2000 {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"status":         status,
		"name":           "LiMe",
		"version":        version.Version,
		"uptime_seconds": int(time.Since(h.startedAt).Seconds()),
		"db":             map[string]any{"ok": dbOK, "latency_ms": latency},
		"goroutines":     runtime.NumGoroutine(),
		"memory":         map[string]any{"alloc_mb": alloc >> 20, "sys_mb": sys >> 20},
	})
}

// runHealthMonitor 每 5 分钟检查磁盘和数据库延迟，异常时推 Telegram。
// 磁盘阈值：剩余 <10% 或 <5GB；数据库：单次 SELECT 1 超过 1 秒。
func (h *Handler) runHealthMonitor() {
	time.Sleep(time.Minute) // 避开启动迁移/索引重建
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		m := h.healthMetrics()
		if m.DiskTotalBytes > 0 {
			pct := float64(m.DiskFreeBytes) * 100 / float64(m.DiskTotalBytes)
			if pct < 10 || m.DiskFreeBytes < 5<<30 {
				h.healthAlert("disk", fmt.Sprintf(
					"💾 数据盘剩余 %.1f%%（%s / %s）",
					pct, humanBytes(m.DiskFreeBytes), humanBytes(m.DiskTotalBytes)))
			}
		}
		if m.DBLatencyMs > 1000 {
			h.healthAlert("db", fmt.Sprintf("🗄 数据库响应 %dms", m.DBLatencyMs))
		}
		<-ticker.C
	}
}

func (h *Handler) healthAlert(kind, detail string) {
	if h.notifier == nil || !h.notifier.EventEnabled("health") {
		return
	}
	h.healthMu.Lock()
	if h.healthAlertAt == nil {
		h.healthAlertAt = make(map[string]time.Time, 4)
	}
	last := h.healthAlertAt[kind]
	if time.Since(last) < 30*time.Minute {
		h.healthMu.Unlock()
		return
	}
	h.healthAlertAt[kind] = time.Now()
	h.healthMu.Unlock()

	h.log.Warn("health alert", "kind", kind, "detail", detail)
	msg := "<b>⚠️ LiMe 健康告警</b>\n" + html.EscapeString(detail) +
		"\n🕐 " + time.Now().Format("01-02 15:04")
	h.notifier.Notify("health", msg)
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
