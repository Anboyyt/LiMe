package api

import (
	"net/http"

	"lime/internal/scrape"
)

// webPosters 查看/启动海报升清晰度任务。
func (h *Handler) webPosters(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if h.scraper == nil {
			writeJSON(w, http.StatusOK, scrape.BackfillStatus{})
			return
		}
		writeJSON(w, http.StatusOK, h.scraper.PosterStatus())
	case http.MethodPost:
		if h.scraper == nil || !h.scraper.Enabled() {
			writeError(w, http.StatusBadRequest, "未配置 TMDB Key，无法下载海报")
			return
		}
		if !h.scraper.StartPosterBackfill() {
			writeError(w, http.StatusConflict, "图片回填正在进行")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
