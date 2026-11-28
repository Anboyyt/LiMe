package api

import (
	"net/http"
	"strconv"
)

// webFeatured 返回首页精选候选：评分占 75%，近 30 天播放次数占 25%。
// 前端拿到 Top N 后按日期固定选一条，保证一天内不重样、跨天会换。
func (h *Handler) webFeatured(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	limit := 8
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 30 {
			limit = n
		}
	}
	items, err := h.db.FeaturedItems(limit)
	if err != nil {
		h.log.Error("featured items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := h.itemsToDto(r, user, items)
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            out,
		"TotalRecordCount": len(out),
	})
}
