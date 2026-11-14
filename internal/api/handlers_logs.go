package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
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

// webLogsExport 把内存里的日志导出成可下载的文本文件（按时间正序）。
func (h *Handler) webLogsExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	level := r.URL.Query().Get("level")
	entries := logbuf.Default().Snapshot(1000, level)
	// Snapshot 是新到旧，导出文件按时间正序更好读。
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	now := time.Now().In(time.Local)
	filename := "lime-logs-" + now.Format("20060102-150405") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	var b strings.Builder
	fmt.Fprintf(&b, "LiMe 运行日志导出\n导出时间: %s\n日志条数: %d（内存缓存上限 1000 条）\n级别过滤: %s\n",
		now.Format("2006-01-02 15:04:05 -0700"), len(entries), orAll(level))
	b.WriteString(strings.Repeat("-", 72))
	b.WriteString("\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s [%s] %s", e.Time.In(time.Local).Format("2006-01-02 15:04:05.000"), e.Level, e.Message)
		if len(e.Attrs) > 0 {
			keys := make([]string, 0, len(e.Attrs))
			for k := range e.Attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, " %s=%s", k, e.Attrs[k])
			}
		}
		b.WriteString("\n")
	}
	_, _ = w.Write([]byte(b.String()))
}

func orAll(s string) string {
	if strings.TrimSpace(s) == "" {
		return "全部"
	}
	return strings.ToUpper(strings.TrimSpace(s))
}
