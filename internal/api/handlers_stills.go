package api

import (
	"net/http"

	"lime/internal/scrape"
)

// webStills 查看/下载/清理单集剧照。
func (h *Handler) webStills(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		if h.scraper == nil {
			writeJSON(w, http.StatusOK, scrape.BackfillStatus{})
			return
		}
		writeJSON(w, http.StatusOK, h.scraper.StillStatus())
	case http.MethodPost:
		if h.scraper == nil || !h.scraper.Enabled() {
			writeError(w, http.StatusBadRequest, "未配置 TMDB Key，无法下载剧照")
			return
		}
		if !h.scraper.EpisodeStills() {
			writeError(w, http.StatusBadRequest, "当前是「继承剧集海报」模式，请先切换为剧照模式")
			return
		}
		if !h.scraper.StartStillBackfill() {
			writeError(w, http.StatusConflict, "图片回填正在进行")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case http.MethodDelete:
		if h.scraper == nil {
			writeError(w, http.StatusBadRequest, "刮削未初始化")
			return
		}
		n, err := h.scraper.CleanupEpisodeStills()
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Removed": n})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
