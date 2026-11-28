package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"lime/internal/store"
)

// webAttention 汇总需要人工处理的条目：没有 TMDB ID、刮削失败、缺图、重复。
func (h *Handler) webAttention(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 默认每类 100 条：一次返回四份列表，200 条时响应能到 70KB+，
	// 外网或慢客户端首屏会明显卡顿。需要更多用 ?limit= 显式要。
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	failureCounts, health, err := h.cachedFailureSummary()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	failures, err := h.db.ScrapeFailures(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	noTmdb, err := h.db.ItemsMissingTmdbID(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	missingArt, err := h.db.ItemsMissingArtwork(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	duplicates, err := h.db.DuplicateTmdbGroups(limit / 2)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if failures == nil {
		failures = []store.ScrapeFailure{}
	}
	if noTmdb == nil {
		noTmdb = []store.AttentionItem{}
	}
	if missingArt == nil {
		missingArt = []store.AttentionItem{}
	}
	if duplicates == nil {
		duplicates = []store.DuplicateGroup{}
	}

	failureTotal := 0
	for _, n := range failureCounts {
		failureTotal += n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Counts": map[string]int{
			"NoTmdb":          health.NoTmdbID,
			"Unscraped":       health.Unscraped,
			"MissingPoster":   health.MissingPoster,
			"MissingBackdrop": health.MissingBackdrop,
			"MissingArtwork":  health.MissingArtwork,
			"Failures":        failureTotal,
			"Duplicates":      len(duplicates),
		},
		"NoTmdb":     noTmdb,
		"Failures":   failures,
		"MissingArt": missingArt,
		"Duplicates": duplicates,
	})
}

// webAttentionRetry 对选中的条目立即重新刮削。
func (h *Handler) webAttentionRetry(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.scraper == nil || !h.scraper.Enabled() {
		writeError(w, http.StatusBadRequest, "未配置 TMDB Key，无法重试刮削")
		return
	}

	var p struct {
		ItemIds []string `json:"ItemIds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ids := make([]string, 0, len(p.ItemIds))
	for _, id := range p.ItemIds {
		id = strings.TrimSpace(id)
		if id != "" && len(ids) < 200 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "请选择要重试的条目")
		return
	}

	h.invalidateFailureSummary()
	go func() {
		if _, err := h.scraper.ScrapeItems(ids); err != nil {
			h.log.Warn("attention retry failed", "items", len(ids), "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"Started": true, "Items": len(ids)})
}
