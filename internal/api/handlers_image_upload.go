package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lime/internal/store"
)

// maxUploadImage 限制单张上传图片大小。
// 动态封面是 APNG：320x180、24fps、8 秒实测 27MB，base64 后约 36MB，
// 所以不能按静态图的几 MB 来卡。
const maxUploadImage = 64 << 20

// uploadItemImage 处理 Emby 的写回图片接口：
//
//	POST /Items/{id}/Images/{type}
//	POST /Items/{id}/Images/{type}/{index}
//
// 目标既可能是媒体条目，也可能是媒体库（生成器给库换封面时用库 ID）。
// Emby 约定成功返回 204，响应体为空。
func (h *Handler) uploadItemImage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("itemId")
	imageType := strings.ToLower(r.PathValue("imageType"))

	it, itemErr := h.db.GetItem(id)
	var lib *store.Library
	if itemErr != nil {
		l, err := h.db.GetLibrary(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
		lib = l
	}
	libID := ""
	if it != nil {
		libID = it.LibraryID
	}
	if lib != nil {
		libID = lib.ID
	}
	if !h.canAccessLibrary(user, libID) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	body, err := readUploadedImage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image body")
		return
	}
	// 部分工具（TgtoDrive 就是）把图片 base64 后再 POST，原样落盘客户端解不出来。
	body = normalizeImageBody(body)

	// 动态封面生成器输出的是 APNG。SenPlayer 这类客户端只认 GIF 动图，
	// APNG 只会显示第一帧，而且 28MB 太大。这里统一转成 GIF（约 2MB）。
	if isAnimatedPNG(body) {
		apngLen := len(body)
		if gif, gerr := apngToGIF(body); gerr == nil {
			body = gif
			h.log.Info("animated cover transcoded", "apng_bytes", apngLen, "gif_bytes", len(gif))
		} else {
			h.log.Warn("apng to gif failed; keep apng", "err", gerr)
		}
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty image body")
		return
	}
	if len(body) > maxUploadImage {
		writeError(w, http.StatusRequestEntityTooLarge, "image too large")
		return
	}
	ext, okExt := imageExtFor(body, r.Header.Get("Content-Type"))
	if !okExt {
		writeError(w, http.StatusBadRequest, "unsupported image format")
		return
	}

	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if lib != nil {
		suffix := ""
		switch imageType {
		case "primary", "":
		case "backdrop":
			suffix = "backdrop"
		default:
			writeError(w, http.StatusBadRequest, "unsupported image type")
			return
		}
		base := lib.ID + "-custom"
		if suffix != "" {
			base += "-" + suffix
		}
		if _, err := writeImageFile(absData, "library-images", base, ext, body); err != nil {
			writeError(w, http.StatusInternalServerError, "save image failed")
			return
		}
		h.log.Info("library image uploaded", "library", lib.Name, "type", imageType, "bytes", len(body))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch imageType {
	case "primary", "backdrop", "logo":
	default:
		writeError(w, http.StatusBadRequest, "unsupported image type")
		return
	}
	// 文件名带时间戳：换图后路径和 ImageTag 都会变，客户端不会继续用旧缓存。
	base := fmt.Sprintf("%s-%s-%d", it.ID, imageType, time.Now().Unix())
	rel, err := writeImageFile(absData, "images", base, ext, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save image failed")
		return
	}
	if err := h.db.SetItemImage(it.ID, imageType, rel); err != nil {
		writeError(w, http.StatusInternalServerError, "save image failed")
		return
	}
	h.log.Info("item image uploaded", "item", it.Name, "type", imageType, "bytes", len(body))
	w.WriteHeader(http.StatusNoContent)
}

// deleteItemImage 处理 DELETE /Items/{id}/Images/{type}[/{index}]。
// 生成器替换封面前可能先删旧图；删完 GET 会回落到自动生成的库封面。
func (h *Handler) deleteItemImage(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("itemId")
	imageType := strings.ToLower(r.PathValue("imageType"))

	it, itemErr := h.db.GetItem(id)
	var lib *store.Library
	if itemErr != nil {
		l, err := h.db.GetLibrary(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
		lib = l
	}
	libID := ""
	if it != nil {
		libID = it.LibraryID
	}
	if lib != nil {
		libID = lib.ID
	}
	if !h.canAccessLibrary(user, libID) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if lib != nil {
		base := lib.ID + "-custom"
		if imageType == "backdrop" {
			base += "-backdrop"
		} else if imageType != "primary" && imageType != "" {
			writeError(w, http.StatusBadRequest, "unsupported image type")
			return
		}
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
			_ = os.Remove(filepath.Join(absData, "library-images", base+ext))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var rel string
	switch imageType {
	case "primary":
		rel = it.ImagePath
	case "backdrop":
		rel = it.BackdropPath
	case "logo":
		rel = it.LogoPath
	default:
		writeError(w, http.StatusBadRequest, "unsupported image type")
		return
	}
	if rel != "" {
		full := filepath.Join(absData, filepath.FromSlash(rel))
		if strings.HasPrefix(full, absData+string(os.PathSeparator)) {
			_ = os.Remove(full)
		}
	}
	if err := h.db.SetItemImage(it.ID, imageType, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readUploadedImage 兼容两种写回方式：Emby 原始的二进制 body，
// 以及部分工具用的 multipart/form-data 文件字段。
func readUploadedImage(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadImage); err != nil {
			return nil, err
		}
		if r.MultipartForm == nil {
			return nil, errors.New("empty form")
		}
		for _, files := range r.MultipartForm.File {
			for _, fh := range files {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				b, rerr := io.ReadAll(io.LimitReader(f, maxUploadImage+1))
				_ = f.Close()
				if rerr != nil {
					return nil, rerr
				}
				if len(b) > 0 {
					return b, nil
				}
			}
		}
		return nil, errors.New("no file in form")
	}
	return io.ReadAll(io.LimitReader(r.Body, maxUploadImage+1))
}

// isAnimatedPNG 判断是否为 APNG（PNG 里带 acTL 动画控制块）。
func isAnimatedPNG(b []byte) bool {
	if len(b) < 8 || b[0] != 0x89 || string(b[1:4]) != "PNG" {
		return false
	}
	n := len(b)
	if n > 1<<20 {
		n = 1 << 20
	}
	return bytes.Contains(b[:n], []byte("acTL"))
}

// apngToGIF 用容器里的 ffmpeg 把 APNG 转成 GIF。
// 12fps / 128 色 / bayer 抖动：实测 28MB APNG -> 1.9MB GIF，10 秒左右。
func apngToGIF(body []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "apng-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in.png")
	out := filepath.Join(dir, "out.gif")
	if err := os.WriteFile(in, body, 0o644); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "apng", "-i", in,
		"-vf", "fps=12,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer:bayer_scale=3",
		"-loop", "0", out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(b)))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("empty gif")
	}
	return data, nil
}

// normalizeImageBody 兼容工具把图片 base64 后上传的情况：
// 常见形态是裸 base64、data URL（data:image/png;base64,xxx），
// 也有包在 JSON 字段里的。能解出真实图片头才替换，否则原样返回。
func normalizeImageBody(b []byte) []byte {
	if looksLikeImage(b) {
		return b
	}
	s := strings.TrimSpace(string(b))

	// JSON 包装：{"image": "<base64>", ...}
	if strings.HasPrefix(s, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			for _, v := range m {
				if str, ok := v.(string); ok {
					if d := decodeImageBase64(str); d != nil {
						return d
					}
				}
			}
		}
	}
	if d := decodeImageBase64(s); d != nil {
		return d
	}
	return b
}

func decodeImageBase64(s string) []byte {
	if i := strings.Index(s, "base64,"); i >= 0 {
		s = s[i+len("base64,"):]
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', ' ', '\t':
		default:
			sb.WriteRune(r)
		}
	}
	s = sb.String()
	if s == "" {
		return nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if d, err := enc.DecodeString(s); err == nil && looksLikeImage(d) {
			return d
		}
	}
	return nil
}

func looksLikeImage(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	switch {
	case b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return true
	case b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return true
	case string(b[:3]) == "GIF":
		return true
	case string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return true
	}
	return false
}

// imageExtFor 以文件头为准判断格式，Content-Type 只在识别不出来时兜底。
func imageExtFor(body []byte, declared string) (string, bool) {
	switch http.DetectContentType(body) {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	}
	if len(body) > 12 && string(body[0:4]) == "RIFF" && string(body[8:12]) == "WEBP" {
		return ".webp", true
	}
	ct := strings.ToLower(declared)
	switch {
	case strings.Contains(ct, "png"):
		return ".png", true
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg", true
	case strings.Contains(ct, "webp"):
		return ".webp", true
	case strings.Contains(ct, "gif"):
		return ".gif", true
	}
	return "", false
}

// writeImageFile 原子写入 data/<dir>/<base><ext>，并清掉同 base 的其他扩展名，
// 避免换格式后旧文件残留。返回相对 data 目录的路径。
func writeImageFile(absData, dir, base, ext string, body []byte) (string, error) {
	fullDir := filepath.Join(absData, dir)
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return "", err
	}
	rel := filepath.ToSlash(filepath.Join(dir, base+ext))
	full := filepath.Join(absData, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, absData+string(os.PathSeparator)) {
		return "", os.ErrPermission
	}

	tmp, err := os.CreateTemp(fullDir, ".up-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	for _, e := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		if e != ext {
			_ = os.Remove(filepath.Join(fullDir, base+e))
		}
	}
	return rel, nil
}
