package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lime/internal/notify"
	"lime/internal/probe"
)

// webSettings 读取/更新运行时配置（目前是 TMDB Key 和代理）。
func (h *Handler) webSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys := h.tmdbKeys()
		hints := make([]string, 0, len(keys))
		for _, k := range keys {
			hints = append(hints, maskKey(k))
		}
		firstHint := ""
		if len(hints) > 0 {
			firstHint = hints[0]
		}
		proxy, _ := h.db.GetSetting("tmdb_proxy")
		if proxy == "" {
			proxy = h.cfg.TMDBProxy
		}
		scanDaily, _ := h.db.GetSetting("scan_daily_at")
		if scanDaily == "" {
			scanDaily = h.cfg.ScanDailyAt
		}
		scrapeDaily, _ := h.db.GetSetting("scrape_daily_at")
		warm := h.warmupConfig()
		backupAt := h.cfg.BackupDailyAt
		if v, err := h.db.GetSetting("backup_daily_at"); err == nil && v != "" {
			if strings.EqualFold(v, "off") {
				backupAt = ""
			} else {
				backupAt = v
			}
		}
		backupKeep := h.cfg.BackupKeep
		if v, err := h.db.GetSetting("backup_keep"); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				backupKeep = n
			}
		}
		pendingTotal, pendingFav, pendingWatching := h.probeCountsSnapshot()
		segmentPending := 0
		if n, err := h.db.SegmentPendingCount(); err == nil {
			segmentPending = n
		}
		tg := h.tgConfig()
		scrapeTimeout := 6 * 3600
		if h.scraper != nil {
			scrapeTimeout = int(h.scraper.Timeout().Seconds())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"TMDBKeySet":            len(keys) > 0,
			"TMDBKeyHint":           firstHint,
			"TMDBKeyHints":          hints,
			"TMDBKeyCount":          len(keys),
			"TMDBProxy":             proxy,
			"ScrapeEnabled":         h.scraper != nil && h.scraper.Enabled(),
			"ScrapeTimeoutSeconds":  scrapeTimeout,
			"ScanDailyAt":           scanDaily,
			"ScrapeDailyAt":         scrapeDaily,
			"RealtimeMonitor":       h.realtime != nil && h.realtime.Enabled(),
			"ProbeWarmup":           warm.Enabled,
			"ProbeScope":            warm.Scope,
			"ProbeWindow":           warmupWindowLabel(warm),
			"ProbeMaxRun":           shortDuration(warm.MaxRun),
			"ProbePendingTotal":     pendingTotal,
			"ProbePendingFavorites": pendingFav,
			"ProbePendingWatching":  pendingWatching,
			"SegmentExtract":        h.segmentExtractEnabled(),
			"SegmentScope":          h.segmentScopeValue(),
			"SegmentAutoAfter":      shortDuration(h.segmentAutoAfter()),
			"SegmentPending":        segmentPending,
			"BackupDailyAt":         backupAt,
			"BackupKeep":            backupKeep,
			"TGEnabled":             tg.Enabled,
			"TGReady":               tg.Token != "" && tg.ChatID != "",
			"TGBotTokenSet":         tg.Token != "",
			"TGBotTokenHint":        maskKey(tg.Token),
			"TGChatID":              tg.ChatID,
			"TGProxy":               tg.Proxy,
			"TGEvents":              notify.EventsString(tg.Events),
		})

	case http.MethodPut:
		var p struct {
			ApiKey        *string `json:"ApiKey"`
			ClearKeys     *bool   `json:"ClearKeys"`
			Proxy         *string `json:"Proxy"`
			ScanDailyAt   *string `json:"ScanDailyAt"`
			ScrapeDailyAt *string `json:"ScrapeDailyAt"`
			// 单次刮削时长上限（秒），0 表示不限。
			ScrapeTimeoutSeconds *int    `json:"ScrapeTimeoutSeconds"`
			RealtimeMonitor      *bool   `json:"RealtimeMonitor"`
			ProbeWarmup          *bool   `json:"ProbeWarmup"`
			ProbeScope           *string `json:"ProbeScope"`
			ProbeWindow          *string `json:"ProbeWindow"`
			ProbeMaxRun          *string `json:"ProbeMaxRun"`
			SegmentExtract       *bool   `json:"SegmentExtract"`
			SegmentScope         *string `json:"SegmentScope"`
			SegmentAutoAfter     *string `json:"SegmentAutoAfter"`
			BackupDailyAt        *string `json:"BackupDailyAt"`
			BackupKeep           *int    `json:"BackupKeep"`
			TGBotToken           *string `json:"TGBotToken"`
			TGChatID             *string `json:"TGChatID"`
			TGProxy              *string `json:"TGProxy"`
			TGEnabled            *bool   `json:"TGEnabled"`
			TGEvents             *string `json:"TGEvents"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		keys := h.tmdbKeys()
		proxy, _ := h.db.GetSetting("tmdb_proxy")
		if proxy == "" {
			proxy = h.cfg.TMDBProxy
		}
		if p.ClearKeys != nil && *p.ClearKeys {
			keys = nil
		}
		if p.ApiKey != nil {
			k := strings.TrimSpace(*p.ApiKey)
			if k != "" {
				exists := false
				for _, x := range keys {
					if x == k {
						exists = true
						break
					}
				}
				if !exists {
					keys = append(keys, k)
				}
			}
		}
		if p.ApiKey != nil || p.ClearKeys != nil {
			if err := h.db.SetSetting("tmdb_api_keys", strings.Join(keys, "\n")); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			// 清掉旧版单 key 设置，避免下次启动又覆盖回来。
			_ = h.db.SetSetting("tmdb_api_key", "")
		}
		if p.Proxy != nil {
			proxy = strings.TrimSpace(*p.Proxy)
			if err := h.db.SetSetting("tmdb_proxy", proxy); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}

		if p.ScanDailyAt != nil {
			at, ok := normalizeDailyTime(*p.ScanDailyAt)
			if !ok {
				writeError(w, http.StatusBadRequest, "每日扫描时间格式应为 HH:MM")
				return
			}
			if err := h.db.SetSetting("scan_daily_at", at); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}
		if p.ScrapeDailyAt != nil {
			at, ok := normalizeDailyTime(*p.ScrapeDailyAt)
			if !ok {
				writeError(w, http.StatusBadRequest, "每日刮削时间格式应为 HH:MM")
				return
			}
			if err := h.db.SetSetting("scrape_daily_at", at); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}
		if p.ScrapeTimeoutSeconds != nil {
			n := *p.ScrapeTimeoutSeconds
			if n < 0 || n > 30*24*3600 {
				writeError(w, http.StatusBadRequest, "单次刮削时长应在 0（不限）到 30 天之间")
				return
			}
			if h.scraper != nil {
				if err := h.scraper.SetTimeoutSeconds(n); err != nil {
					writeError(w, http.StatusInternalServerError, "save setting failed")
					return
				}
			}
		}

		probeChanged := false
		if p.ProbeScope != nil {
			scope := strings.ToLower(strings.TrimSpace(*p.ProbeScope))
			switch scope {
			case "favorites", "watching", "recent", "all":
			default:
				writeError(w, http.StatusBadRequest, "预热范围只能是 favorites/recent/all")
				return
			}
			if err := h.db.SetSetting("probe_scope", scope); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			probeChanged = true
		}
		if p.ProbeWindow != nil {
			window := strings.TrimSpace(*p.ProbeWindow)
			if window == "" || strings.EqualFold(window, "all") {
				window = "all"
			} else {
				if _, _, ok := probe.ParseWindow(window); !ok {
					writeError(w, http.StatusBadRequest, "时间窗格式应为 HH:MM-HH:MM，或 all 表示全天")
					return
				}
			}
			if err := h.db.SetSetting("probe_window", window); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			probeChanged = true
		}
		if p.ProbeMaxRun != nil {
			v := strings.TrimSpace(*p.ProbeMaxRun)
			if v == "" {
				v = "0"
			}
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				writeError(w, http.StatusBadRequest, "单次最长运行时长格式应为 2h、90m 或 0")
				return
			}
			if err := h.db.SetSetting("probe_max_run", d.String()); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			probeChanged = true
		}
		if p.ProbeWarmup != nil {
			if err := h.db.SetSetting("probe_warmup", strconv.FormatBool(*p.ProbeWarmup)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			probeChanged = true
		}
		segmentChanged := false
		if p.SegmentExtract != nil {
			if err := h.db.SetSetting("segment_extract", strconv.FormatBool(*p.SegmentExtract)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			segmentChanged = true
		}
		if p.SegmentScope != nil {
			scope := strings.ToLower(strings.TrimSpace(*p.SegmentScope))
			switch scope {
			case "favorites", "watching", "recent", "all":
			default:
				writeError(w, http.StatusBadRequest, "片段范围只能是 favorites/watching/recent/all")
				return
			}
			if err := h.db.SetSetting("segment_scope", scope); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			segmentChanged = true
		}
		if p.SegmentAutoAfter != nil {
			v := strings.TrimSpace(*p.SegmentAutoAfter)
			if v == "" || strings.EqualFold(v, "off") || v == "0" {
				v = "0"
			} else {
				d, err := time.ParseDuration(v)
				if err != nil || d < 0 || d > 6*time.Hour {
					writeError(w, http.StatusBadRequest, "自动触发时长格式应为 5m、1h，或 0 表示关闭")
					return
				}
				v = d.String()
			}
			if err := h.db.SetSetting("segment_auto_after", v); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			segmentChanged = true
		}
		if probeChanged || segmentChanged {
			h.prober.SetWarmupConfig(h.warmupConfig())
		}
		if segmentChanged {
			h.prober.SetSegmentsEnabled(h.segmentExtractEnabled())
		}

		if p.BackupDailyAt != nil {
			v := strings.TrimSpace(*p.BackupDailyAt)
			if strings.EqualFold(v, "off") || v == "" {
				v = "off"
			} else if at, ok := normalizeDailyTime(v); ok {
				v = at
			} else {
				writeError(w, http.StatusBadRequest, "备份时间格式应为 HH:MM，或 off 表示关闭")
				return
			}
			if err := h.db.SetSetting("backup_daily_at", v); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}
		if p.BackupKeep != nil {
			if *p.BackupKeep < 1 || *p.BackupKeep > 30 {
				writeError(w, http.StatusBadRequest, "备份保留份数应在 1-30 之间")
				return
			}
			if err := h.db.SetSetting("backup_keep", strconv.Itoa(*p.BackupKeep)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}

		if p.RealtimeMonitor != nil && h.realtime != nil {
			if err := h.realtime.SetEnabled(*p.RealtimeMonitor); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}

		tgChanged := false
		if p.TGBotToken != nil {
			// 留空表示不修改，避免面板回显掩码时把真 token 覆盖掉。
			if v := strings.TrimSpace(*p.TGBotToken); v != "" {
				if err := h.db.SetSetting("tg_bot_token", v); err != nil {
					writeError(w, http.StatusInternalServerError, "save setting failed")
					return
				}
				tgChanged = true
			}
		}
		if p.TGChatID != nil {
			if err := h.db.SetSetting("tg_chat_id", strings.TrimSpace(*p.TGChatID)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			tgChanged = true
		}
		if p.TGProxy != nil {
			if err := h.db.SetSetting("tg_proxy", strings.TrimSpace(*p.TGProxy)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			tgChanged = true
		}
		if p.TGEnabled != nil {
			if err := h.db.SetSetting("tg_enabled", strconv.FormatBool(*p.TGEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			tgChanged = true
		}
		if p.TGEvents != nil {
			raw := strings.ToLower(strings.TrimSpace(*p.TGEvents))
			if raw == "" {
				raw = "all"
			}
			normalized := notify.EventsString(notify.ParseEvents(raw))
			if raw != "all" && raw != "none" && normalized == "none" {
				writeError(w, http.StatusBadRequest, "通知事件只能是 start/pause/resume/stop，或 all / none")
				return
			}
			if normalized == "" {
				normalized = "all"
			}
			if err := h.db.SetSetting("tg_events", normalized); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
			tgChanged = true
		}
		if tgChanged && h.notifier != nil {
			h.notifier.SetConfig(h.tgConfig())
		}

		if h.scraper != nil {
			if err := h.scraper.SetCredentials(keys, proxy); err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// normalizeDailyTime 校验并统一成 HH:MM；空字符串表示关闭。
func normalizeDailyTime(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return "", false
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", h, m), true
}

// tmdbKeys 返回当前生效的 key 列表：数据库优先，其次环境变量。
func (h *Handler) tmdbKeys() []string {
	if v, err := h.db.GetSetting("tmdb_api_keys"); err == nil && v != "" {
		keys := make([]string, 0, 4)
		for _, k := range strings.Split(v, "\n") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			return keys
		}
	}
	if v, err := h.db.GetSetting("tmdb_api_key"); err == nil && v != "" {
		return []string{v}
	}
	return h.cfg.TMDBAPIKeys
}

// warmupConfig 合并环境变量与面板设置（面板优先）。
func (h *Handler) warmupConfig() probe.WarmupConfig {
	c := probe.WarmupConfig{
		Enabled:      h.cfg.ProbeWarmup,
		Scope:        h.cfg.ProbeScope,
		Batch:        h.cfg.ProbeWarmupLimit,
		MaxRun:       h.cfg.ProbeMaxRun,
		StartMin:     -1,
		EndMin:       -1,
		Segments:     h.segmentExtractEnabled(),
		SegmentScope: h.segmentScopeValue(),
	}
	window := h.cfg.ProbeWindow
	if v, err := h.db.GetSetting("probe_warmup"); err == nil && v != "" {
		c.Enabled = v == "true" || v == "1"
	}
	if v, err := h.db.GetSetting("probe_scope"); err == nil && v != "" {
		c.Scope = v
	}
	if v, err := h.db.GetSetting("probe_window"); err == nil && v != "" {
		window = v
	}
	if window != "" && !strings.EqualFold(window, "all") {
		if s, e, ok := probe.ParseWindow(window); ok {
			c.StartMin, c.EndMin = s, e
		}
	}
	if v, err := h.db.GetSetting("probe_max_run"); err == nil && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.MaxRun = d
		}
	}
	return c
}

// segmentExtractEnabled 合并环境变量与面板设置（面板优先）。
func (h *Handler) segmentExtractEnabled() bool {
	if v, err := h.db.GetSetting("segment_extract"); err == nil && v != "" {
		return v == "true" || v == "1"
	}
	return h.cfg.SegmentExtract
}

func (h *Handler) segmentScopeValue() string {
	if v, err := h.db.GetSetting("segment_scope"); err == nil && v != "" {
		switch strings.ToLower(v) {
		case "favorites", "watching", "recent", "all":
			return strings.ToLower(v)
		}
	}
	return h.cfg.SegmentScope
}

func (h *Handler) segmentAutoAfter() time.Duration {
	if v, err := h.db.GetSetting("segment_auto_after"); err == nil && v != "" {
		if strings.EqualFold(v, "off") || v == "0" {
			return 0
		}
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return h.cfg.SegmentAutoAfter
}

func shortDuration(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

func warmupWindowLabel(c probe.WarmupConfig) string {
	if c.StartMin < 0 || c.EndMin < 0 || c.StartMin == c.EndMin {
		return "all"
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", c.StartMin/60, c.StartMin%60, c.EndMin/60, c.EndMin%60)
}
