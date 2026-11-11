package api

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

// itemImage 提供海报/背景图。图是刮削时下载到 data/images 的本地文件，
// 不走网盘也不依赖 TMDB 在线可用性。
func (h *Handler) itemImage(w http.ResponseWriter, r *http.Request) {
	it, err := h.db.GetItem(r.PathValue("itemId"))
	if err != nil {
		// 库本身也有 ID，客户端会请求 /Items/{libraryId}/Images/Primary。
		if lib, lerr := h.db.GetLibrary(r.PathValue("itemId")); lerr == nil {
			h.serveLibraryImage(w, r, lib)
			return
		}
		// 演员不是 media item，用独立的 person_images 表兜底。
		if strings.EqualFold(r.PathValue("imageType"), "primary") {
			if p, perr := h.db.PersonImageByID(r.PathValue("itemId")); perr == nil {
				h.servePersonImageFile(w, r, p)
				return
			}
		}
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	if !h.imageAccessAllowed(r, it.LibraryID) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if tok := parseAuth(r).Token; tok != "" {
		if u, err := h.db.UserByToken(tok); err == nil && !ratingAllowed(u, it) {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
	}

	var rel string
	switch strings.ToLower(r.PathValue("imageType")) {
	case "primary":
		rel = it.ImagePath
	case "backdrop":
		rel = it.BackdropPath
	case "logo":
		rel = it.LogoPath
	default:
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	if rel == "" {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}

	full, ok := h.resolveItemImagePath(rel)
	if !ok {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	h.serveItemImageFile(w, r, full)
}

// resolveItemImagePath 把 items.image_path 解析成真实文件路径，支持两种来源：
//
//	local:/abs/path      媒体目录里自带的本地海报，原地读取不复制
//	images/<hash>.jpg    刮削时下载到 data/images 的图片（相对 DataDir）
func (h *Handler) resolveItemImagePath(rel string) (string, bool) {
	if strings.HasPrefix(rel, "local:") {
		full := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(rel, "local:")))
		if !h.localMediaImageAllowed(full) {
			return "", false
		}
		st, err := os.Stat(full)
		if err != nil || !st.Mode().IsRegular() {
			return "", false
		}
		return full, true
	}

	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return "", false
	}
	full := filepath.Join(absData, filepath.FromSlash(rel))
	// 防目录穿越：拼出来的路径必须还在 data 目录下
	if !strings.HasPrefix(full, absData+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}

// localMediaImageAllowed 只允许读取媒体库根目录下的文件，防止 local: 路径穿越。
func (h *Handler) localMediaImageAllowed(full string) bool {
	if !filepath.IsAbs(full) {
		return false
	}
	roots := h.cfg.LibraryRoots
	if len(roots) == 0 {
		libs, err := h.db.ListLibraries()
		if err != nil {
			return false
		}
		for _, l := range libs {
			if len(l.Paths) > 0 {
				roots = append(roots, l.Paths...)
			} else {
				roots = append(roots, l.Path)
			}
		}
	}
	for _, root := range roots {
		r := filepath.Clean(root)
		if r == "" || r == "." {
			continue
		}
		if full == r || strings.HasPrefix(full, r+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func (h *Handler) serveItemImageFile(w http.ResponseWriter, r *http.Request, full string) {
	w.Header().Set("Cache-Control", "public, max-age=604800")
	// logo 是带透明通道的 PNG/SVG，缩放缓存会转成 JPEG 丢 alpha，直接发原图。
	if strings.EqualFold(r.PathValue("imageType"), "logo") {
		http.ServeFile(w, r, full)
		return
	}
	// 动图缩放后只剩第一帧，直接发原文件。
	if strings.EqualFold(filepath.Ext(full), ".gif") {
		http.ServeFile(w, r, full)
		return
	}
	maxW := atoiDefault(r.URL.Query().Get("maxWidth"), 0)
	maxH := atoiDefault(r.URL.Query().Get("maxHeight"), 0)
	if maxW > 0 || maxH > 0 {
		h.serveScaledImage(w, r, full, maxW, maxH)
		return
	}
	http.ServeFile(w, r, full)
}

// serveScaledImage 按客户端请求的 maxWidth/maxHeight 等比缩放并缓存。
// Jellyfin 客户端普遍会带这两个参数，原图动辄几 MB，手机上加载很慢。
func (h *Handler) serveScaledImage(w http.ResponseWriter, r *http.Request, full string, maxW, maxH int) {
	if maxW <= 0 {
		maxW = 4096
	}
	if maxH <= 0 {
		maxH = 4096
	}
	if maxW > 4096 {
		maxW = 4096
	}
	if maxH > 4096 {
		maxH = 4096
	}

	cacheDir := filepath.Join(h.cfg.DataDir, "image-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		http.ServeFile(w, r, full)
		return
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", full, maxW, maxH)))
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".jpg")
	if st, err := os.Stat(cachePath); err == nil && st.Size() > 0 {
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, r, cachePath)
		return
	}

	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, _, err := image.Decode(f)
	_ = f.Close()
	if err != nil {
		// 解码不了就退回原图，不影响客户端。
		http.ServeFile(w, r, full)
		return
	}

	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		http.ServeFile(w, r, full)
		return
	}
	scale := math.Min(float64(maxW)/float64(srcW), float64(maxH)/float64(srcH))
	if scale >= 1 {
		http.ServeFile(w, r, full)
		return
	}
	dstW := int(float64(srcW) * scale)
	dstH := int(float64(srcH) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	tmp, err := os.CreateTemp(cacheDir, "img-*.jpg")
	if err != nil {
		http.ServeFile(w, r, full)
		return
	}
	tmpName := tmp.Name()
	if err := jpeg.Encode(tmp, dst, &jpeg.Options{Quality: 85}); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		http.ServeFile(w, r, full)
		return
	}
	_ = tmp.Close()
	if err := os.Rename(tmpName, cachePath); err != nil {
		_ = os.Remove(tmpName)
		http.ServeFile(w, r, full)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, cachePath)
}
