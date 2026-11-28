package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"lime/internal/store"
)

// downloadEnabled 是下载能力的唯一开关，用户策略、条目 CanDownload 和
// 下载端点都从这里取值，避免客户端看到按钮却被端点拒绝。
func (h *Handler) downloadEnabled() bool {
	return h.cfg.DownloadMode != "off"
}

// downloadItem 对应 Jellyfin/Emby 的 GET /Items/{id}/Download 和 /File。
// STRM 片源没有本地文件，默认 302 到 CDN；LIME_DOWNLOAD_MODE=proxy 时
// 才由 LiMe 中转字节，供不跟 302 的下载器使用。
func (h *Handler) downloadItem(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if !h.downloadEnabled() || !user.AllowDownload {
		writeError(w, http.StatusForbidden, "downloading is disabled")
		return
	}

	itemID := r.PathValue("itemId")
	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if !h.canAccessLibrary(user, it.LibraryID) || !ratingAllowed(user, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		h.log.Error("load media sources", "item", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(sources) == 0 {
		writeError(w, http.StatusNotFound, "no media source")
		return
	}
	for _, s := range sources {
		h.refreshSourceFromSTRM(s)
	}

	sourceID := queryAny(r, "MediaSourceId", "mediaSourceId", "MediaSourceID")
	src := pickMediaSource(sources, sourceID)
	if src == nil || strings.TrimSpace(src.URL) == "" {
		writeError(w, http.StatusNotFound, "media source not found")
		return
	}

	name := downloadName(it, src)
	w.Header().Set("Content-Disposition", attachmentHeader(name))
	w.Header().Set("Cache-Control", "no-store")

	location := h.streamLocation(r, src.URL)

	// 下载器不跟 302 时用中转；正常情况仍让客户端直连 CDN。
	if h.cfg.DownloadMode == "proxy" {
		h.log.Info("download proxy",
			"item", itemID, "source", src.ID, "cloud", src.Cloud,
			"range", r.Header.Get("Range"), "remote", r.RemoteAddr)
		h.proxyStream(w, r, src, location)
		return
	}

	if h.resolveRedirect.Load() {
		// 和播放一样：解析失败回退原始 302，不返回 502 把下载卡死。
		final, err := h.resolveURL(r.Context(), location, r.UserAgent(), autoResolveTimeout)
		if err != nil {
			if !errors.Is(err, errAutoResolveBlocked) {
				h.log.Warn("download resolve fallback", "item", itemID, "err", err)
			}
		} else if final != "" {
			location = safeLocation(final)
		}
	}

	h.log.Info("download redirect",
		"item", itemID, "source", src.ID, "cloud", src.Cloud,
		"range", r.Header.Get("Range"), "remote", r.RemoteAddr,
	)
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func downloadName(it *store.Item, src *store.MediaSource) string {
	name := strings.TrimSpace(src.FileName)
	if name == "" {
		name = strings.TrimSpace(it.Name)
		ext := strings.TrimSpace(src.Container)
		if ext != "" && !strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(ext)) {
			name += "." + ext
		}
	}
	// 头里的引号和换行会破坏 Content-Disposition。
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return "download"
	}
	return name
}

func attachmentHeader(name string) string {
	// filename* 用 RFC 5987 编码中文；filename 给只认 ASCII 的老客户端兜底。
	fallback := "download" + filepath.Ext(name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		fallback, url.PathEscape(name))
}
