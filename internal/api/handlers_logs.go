package api

import (
	"net/http"
	"time"

	"lime/internal/logbuf"
)

// webLogs 返回最近的内存日志，供运维面板查看。
func (h *Handler) webLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	v := r.URL.Query()
	limit := atoiDefault(v.Get("limit"), 300)
	if limit <= 0 {
		limit = 300
	}
	if limit > 1000 {
		limit = 1000
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Entries":    logbuf.Default().Snapshot(limit, v.Get("level")),
		"ServerTime": time.Now().UTC().Format(time.RFC3339Nano),
	})
}
