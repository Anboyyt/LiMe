package api

import "net/http"

// webStorage 查看/清理 data/images 与 image-cache。
func (h *Handler) webStorage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.scraper == nil {
		writeError(w, http.StatusBadRequest, "刮削未初始化")
		return
	}

	switch r.Method {
	case http.MethodGet:
		stats, err := h.scraper.StorageStats()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, stats)
	case http.MethodPost:
		res, err := h.scraper.CleanupStorage()
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
