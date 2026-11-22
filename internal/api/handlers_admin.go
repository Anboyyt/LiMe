package api

import (
	"net/http"
	"time"

	"lime/internal/library"
	"lime/internal/scrape"
	"lime/internal/store"
	"lime/internal/version"
)

// webStats 是运维面板首页的数据。只读，不涉及播放。
type webStats struct {
	ServerName string `json:"ServerName"`
	// Version 是伪装给 Jellyfin 客户端的协议版本，客户端会做兼容校验。
	Version string `json:"Version"`
	// ProductVersion 是 LiMe 自身的发布版本，仅用于展示。
	ProductVersion string         `json:"ProductVersion"`
	ServerID       string         `json:"ServerId"`
	StartedAt      int64          `json:"StartedAt"`
	UptimeSec      int64          `json:"UptimeSec"`
	Scan           library.Status `json:"Scan"`
	Scrape         scrape.Status  `json:"Scrape"`
	ScrapeEnabled  bool           `json:"ScrapeEnabled"`
	WatchDirs      int            `json:"WatchDirs"`
	DiskFreeBytes  uint64         `json:"DiskFreeBytes"`
	DiskTotalBytes uint64         `json:"DiskTotalBytes"`
	DBLatencyMs    int64          `json:"DBLatencyMs"`
	Goroutines     int            `json:"Goroutines"`
	Stats          *store.Stats   `json:"Stats"`
}

func (h *Handler) realtimeDirs() int {
	if h.realtime == nil {
		return 0
	}
	return h.realtime.WatchDirs()
}

const statsCacheTTL = 60 * time.Second

// cachedStats 缓存全库统计。COUNT 子查询要扫几十万行，面板来回切换时
// 没必要每次都重算；库有增删改时调用 invalidateStats 立即失效。
func (h *Handler) cachedStats() (*store.Stats, error) {
	ttl := statsCacheTTL
	var lastFinished int64
	if h.scanner != nil {
		st := h.scanner.Status()
		if st.Running {
			// 扫描过程中计数一直在变，缓存短一点，页面能跟上进度。
			ttl = 3 * time.Second
		}
		lastFinished = st.LastFinished
	}

	h.statsMu.Lock()
	if h.statsCache != nil && time.Since(h.statsCacheAt) < ttl &&
		h.statsCacheAt.Unix() >= lastFinished {
		s := h.statsCache
		h.statsMu.Unlock()
		return s, nil
	}
	h.statsMu.Unlock()

	s, err := h.db.Stats()
	if err != nil {
		return nil, err
	}
	h.statsMu.Lock()
	h.statsCache = s
	h.statsCacheAt = time.Now()
	h.statsMu.Unlock()
	return s, nil
}

func (h *Handler) invalidateStats() {
	h.statsMu.Lock()
	h.statsCache = nil
	h.statsCacheAt = time.Time{}
	h.statsMu.Unlock()
}

func (h *Handler) webStatsHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	stats, err := h.cachedStats()
	if err != nil {
		h.log.Error("load stats", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var scrapeStatus scrape.Status
	if h.scraper != nil {
		scrapeStatus = h.scraper.Status()
	}

	health := h.healthMetrics()
	writeJSON(w, http.StatusOK, webStats{
		ServerName:     h.cfg.ServerName,
		Version:        h.cfg.Version,
		ProductVersion: version.Version,
		ServerID:       h.serverID,
		StartedAt:      h.startedAt.Unix(),
		UptimeSec:      int64(time.Since(h.startedAt).Seconds()),
		Scan:           h.scanner.Status(),
		Scrape:         scrapeStatus,
		ScrapeEnabled:  h.scraper != nil && h.scraper.Enabled(),
		WatchDirs:      h.realtimeDirs(),
		DiskFreeBytes:  health.DiskFreeBytes,
		DiskTotalBytes: health.DiskTotalBytes,
		DBLatencyMs:    health.DBLatencyMs,
		Goroutines:     health.Goroutines,
		Stats:          stats,
	})
}

// webScan 返回扫描状态；POST 另外触发一次异步全量扫描。
func (h *Handler) webScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	if r.Method == http.MethodPost {
		if h.scanner.Status().Running {
			writeJSON(w, http.StatusConflict, map[string]any{
				"Started": false,
				"Reason":  "scan already running",
				"Scan":    h.scanner.Status(),
			})
			return
		}
		go func() {
			count, err := h.scanner.ScanAll()
			h.invalidateFilterCache()
			if err != nil {
				h.log.Error("library refresh failed", "err", err)
				return
			}
			h.log.Info("library refresh done", "strm", count)
		}()
		// 让 UI 立刻看到 running=true，避免连点两次都显示「已开始」。
		writeJSON(w, http.StatusAccepted, map[string]any{"Started": true})
		return
	}

	writeJSON(w, http.StatusOK, h.scanner.Status())
}
