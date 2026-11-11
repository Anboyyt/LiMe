package api

import (
	"net/http"

	"lime/internal/scrape"
)

// webBackdrops 查看/启动背景图原图回填。
func (h *Handler) webBackdrops(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if h.scraper == nil {
			writeJSON(w, http.StatusOK, scrape.BackdropStatus{})
			return
		}
		writeJSON(w, http.StatusOK, h.scraper.BackdropStatus())
	case http.MethodPost:
		if h.scraper == nil || !h.scraper.Enabled() {
			writeError(w, http.StatusBadRequest, "未配置 TMDB Key，无法下载背景图")
			return
		}
		if !h.scraper.StartBackdropBackfill() {
			writeError(w, http.StatusConflict, "背景图回填正在进行")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
