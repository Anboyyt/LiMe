package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"lime/internal/strm"
)

// subtitleStream 提供外挂字幕文件。
// 兼容 Jellyfin/Emby 的两种路径：
//
//	/Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/Stream.{format}
//	/Videos/{itemId}/Subtitles/{index}/Stream.{format}
func (h *Handler) subtitleStream(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.PathValue("action")), "stream.") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if h.cfg.StreamRequireAuth {
		if _, ok := h.requireUser(w, r); !ok {
			return
		}
	}

	itemID := r.PathValue("itemId")
	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	src := pickMediaSource(sources, r.PathValue("mediaSourceId"))
	if src == nil {
		writeError(w, http.StatusNotFound, "media source not found")
		return
	}

	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusNotFound, "subtitle not found")
		return
	}
	// 客户端带回来的是 MediaStream.Index（基址 + 外挂字幕序号），
	// 容器内嵌字幕的序号没有对应文件，直接 404。
	pos := index - externalSubtitleIndexBase
	subs := strm.FindSubtitles(src.StrmPath)
	if pos < 0 || pos >= len(subs) {
		writeError(w, http.StatusNotFound, "subtitle not found")
		return
	}

	data, err := os.ReadFile(subs[pos].Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "subtitle not found")
		return
	}

	w.Header().Set("Content-Type", subtitleContentType(subs[pos].Format))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

func subtitleContentType(format string) string {
	switch strings.ToLower(format) {
	case "srt":
		return "application/x-subrip; charset=utf-8"
	case "vtt":
		return "text/vtt; charset=utf-8"
	case "ass", "ssa":
		return "text/x-ssa; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}
