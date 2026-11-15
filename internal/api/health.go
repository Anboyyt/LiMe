package api

import (
	"fmt"
	"html"
	"runtime"
	"time"
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
