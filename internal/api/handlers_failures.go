package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"lime/internal/store"
)

// webScrapeFailures 输出刮削失败报告：JSON 给面板展示，CSV 给人工排查。
func (h *Handler) webScrapeFailures(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	counts, err := h.db.ScrapeFailureCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	health, err := h.db.ScrapeHealth()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if r.URL.Query().Get("format") == "csv" {
		items, err := h.db.ScrapeFailures(20000)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="scrape-failures.csv"`)
		// Excel 打开 UTF-8 CSV 需要 BOM
		_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"类型", "名称", "TMDB ID", "条目 ID", "片源路径", "问题", "详情", "更新时间"})
		for _, f := range items {
			_ = cw.Write([]string{
				failureTypeLabel(f.Type), f.Name, f.TmdbID, f.ItemID, f.Path,
				failureKindLabel(f.Kind), f.Detail,
				time.Unix(f.UpdatedAt, 0).Format("2006-01-02 15:04:05"),
			})
		}
		cw.Flush()
		return
	}

	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.db.ScrapeFailures(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if items == nil {
		items = []store.ScrapeFailure{} // 数组不能是 null
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Counts": counts,
		"Health": health,
		"Items":  items,
	})
}

func failureTypeLabel(t string) string {
	switch t {
	case "Movie":
		return "电影"
	case "Series":
		return "剧集"
	case "Season":
		return "季"
	case "Episode":
		return "单集"
	}
	return t
}

func failureKindLabel(k string) string {
	switch k {
	case "not_found":
		return "TMDB ID 失效"
	case "api_error":
		return "TMDB 接口错误"
	case "poster":
		return "海报下载失败"
	case "backdrop":
		return "背景图下载失败"
	case "logo":
		return "标题图下载失败"
	}
	return k
}
