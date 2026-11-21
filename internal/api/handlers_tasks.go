package api

import (
	"net/http"
	"strconv"

	"lime/internal/store"
)

// webTaskHistory 返回最近的扫描/刮削/备份执行记录。
func (h *Handler) webTaskHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	runs, err := h.db.TaskRuns(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if runs == nil {
		runs = []store.TaskRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}
