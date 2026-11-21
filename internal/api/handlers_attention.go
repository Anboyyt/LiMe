package api

import (
	"encoding/json"
	"net/http"
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

	health, err := h.db.ScrapeHealth()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	failureCounts, err := h.db.ScrapeFailureCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	failures, err := h.db.ScrapeFailures(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	noTmdb, err := h.db.ItemsMissingTmdbID(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	missingArt, err := h.db.ItemsMissingArtwork(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	duplicates, err := h.db.DuplicateTmdbGroups(100)
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

	go func() {
		if _, err := h.scraper.ScrapeItems(ids); err != nil {
			h.log.Warn("attention retry failed", "items", len(ids), "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"Started": true, "Items": len(ids)})
}
