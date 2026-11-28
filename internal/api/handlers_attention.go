package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"lime/internal/store"
)

// webAttention 汇总需要人工处理的条目。
//
// 首屏只返回计数（各类列表留空），前端按标签页再请求 ?kind=xxx 拉具体列表：
// 四类全查要 0.6 秒以上，首屏等全部数据会明显卡顿。
func (h *Handler) webAttention(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	kind := strings.ToLower(r.URL.Query().Get("kind"))

	failureCounts, health, err := h.cachedFailureSummary()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	dupCount, err := h.cachedDuplicateCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	failureTotal := 0
	for _, n := range failureCounts {
		failureTotal += n
	}
	resp := map[string]any{
		"Counts": map[string]int{
			"NoTmdb":          health.NoTmdbID,
			"Unscraped":       health.Unscraped,
			"MissingPoster":   health.MissingPoster,
			"MissingBackdrop": health.MissingBackdrop,
			"MissingArtwork":  health.MissingArtwork,
			"Failures":        failureTotal,
			"Duplicates":      dupCount,
		},
		"NoTmdb":     []store.AttentionItem{},
		"Failures":   []store.ScrapeFailure{},
		"MissingArt": []store.AttentionItem{},
		"Duplicates": []store.DuplicateGroup{},
	}

	switch kind {
	case "":
		// 只要计数
	case "notmdb":
		items, err := h.db.ItemsMissingTmdbID(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if items == nil {
			items = []store.AttentionItem{}
		}
		resp["NoTmdb"] = items
	case "failures":
		items, err := h.db.ScrapeFailures(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if items == nil {
			items = []store.ScrapeFailure{}
		}
		resp["Failures"] = items
	case "missing":
		items, err := h.db.ItemsMissingArtwork(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if items == nil {
			items = []store.AttentionItem{}
		}
		resp["MissingArt"] = items
	case "dups":
		items, err := h.db.DuplicateTmdbGroups(limit / 2)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if items == nil {
			items = []store.DuplicateGroup{}
		}
		resp["Duplicates"] = items
	default:
		writeError(w, http.StatusBadRequest, "unknown kind")
		return
	}
	writeJSON(w, http.StatusOK, resp)
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
