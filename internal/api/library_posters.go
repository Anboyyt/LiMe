package api

import (
	"bytes"
	"crypto/sha1"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lime/internal/store"
)

// libraryArtwork 返回库封面：自定义上传 > 简约模糊封面 > 库内最新海报。
// 路径都相对 data 目录，tag 用于客户端缓存失效。
func (h *Handler) libraryArtwork(lib *store.Library) (primaryRel, primaryTag, backdropRel, backdropTag string) {
	// 工具（封面生成器）上传过自定义封面时优先用它，不再用库内最新海报。
	if rel, tag := h.customLibraryImage(lib.ID, ""); rel != "" {
		backdropRel, backdropTag = h.customLibraryImage(lib.ID, "backdrop")
		return rel, tag, backdropRel, backdropTag
	}

	// 简约封面：最新一张海报模糊压暗后叠库名。设置 library_cover_style=latest 可回退。
	// Backdrop 复用同一张图：Sen 等客户端库卡片优先读 Backdrop，若这里返回条目
	// 自己的 fanart，看到的会是原始剧照拼贴而不是生成的库封面。
	if h.libraryCoverStyle() != "latest" {
		if rel, tag, _, err := h.libraryCoverPoster(lib); err == nil && rel != "" {
			return rel, tag, rel, tag
		} else if err != nil {
			h.log.Warn("library cover failed", "library", lib.ID, "err", err)
		}
	}

	candidates, err := h.db.LibraryPosterCandidates(lib.ID, 5)
	if err != nil {
		return "", "", "", ""
	}
	for _, it := range candidates {
		if it.ImagePath == "" {
			continue
		}
		src, ok := h.resolveItemImagePath(it.ImagePath)
		if !ok {
			continue
		}
		if st, err := os.Stat(src); err != nil || st.Size() == 0 {
			continue
		}
		tag := imageTag(it.ImagePath)
		ext := strings.ToLower(filepath.Ext(it.ImagePath))
		if ext == "" {
			ext = ".jpg"
		}
		rel := filepath.ToSlash(filepath.Join("library-images", lib.ID+"-"+tag+ext))
		if err := h.cacheLibraryImage(lib.ID, it.ImagePath, rel); err != nil {
			h.log.Warn("cache library poster failed", "library", lib.ID, "err", err)
			continue
		}
		primaryRel, primaryTag = rel, tag
		backdropRel = h.validArtworkRel(it.BackdropPath)
		if backdropRel != "" {
			backdropTag = imageTag(backdropRel)
		}
		break
	}

	// 还没刮削/没有海报时，生成一张带 LiMe logo 的渐变封面。
	if primaryRel == "" {
		if rel, tag, err := h.defaultLibraryPoster(lib); err == nil {
			primaryRel, primaryTag = rel, tag
		}
	}
	return primaryRel, primaryTag, backdropRel, backdropTag
}

// customLibraryImage 返回上传到 data/library-images 的库封面。
// suffix 为空是 Primary，backdrop 是背景图。tag 带文件时间，换图后客户端会重新拉。
func (h *Handler) customLibraryImage(libID, suffix string) (string, string) {
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return "", ""
	}
	base := libID + "-custom"
	if suffix != "" {
		base += "-" + suffix
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		full := filepath.Join(absData, "library-images", base+ext)
		st, err := os.Stat(full)
		if err != nil || st.Size() == 0 {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("library-images", base+ext))
		return rel, imageTag(rel + ":" + strconv.FormatInt(st.ModTime().UnixNano(), 10))
	}
	return "", ""
}

func (h *Handler) defaultLibraryPoster(lib *store.Library) (string, string, error) {
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return "", "", err
	}
	rel := filepath.ToSlash(filepath.Join("library-images", lib.ID+"-default.jpg"))
	full := filepath.Join(absData, filepath.FromSlash(rel))
	if st, err := os.Stat(full); err == nil && st.Size() > 0 {
		return rel, imageTag("default:" + lib.ID), nil
	}

	const w, hgt = 500, 750
	img := image.NewRGBA(image.Rect(0, 0, w, hgt))
	sum := sha1.Sum([]byte(lib.ID))
	c1 := color.RGBA{R: 24 + sum[0]%60, G: 40 + sum[1]%80, B: 70 + sum[2]%100, A: 255}
	c2 := color.RGBA{R: 60 + sum[3]%100, G: 100 + sum[4]%100, B: 130 + sum[5]%100, A: 255}
	for y := 0; y < hgt; y++ {
		for x := 0; x < w; x++ {
			t := float64(x+y) / float64(w+hgt)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(c1.R)*(1-t) + float64(c2.R)*t),
				G: uint8(float64(c1.G)*(1-t) + float64(c2.G)*t),
				B: uint8(float64(c1.B)*(1-t) + float64(c2.B)*t),
				A: 255,
			})
		}
	}

	// logo 居中放大
	if logo, err := decodePNG(logoPNG); err == nil {
		b := logo.Bounds()
		sw, sh := b.Dx(), b.Dy()
		tw := 210
		th := tw * sh / sw
		if th > 260 {
			th = 260
			tw = th * sw / sh
		}
		ox, oy := (w-tw)/2, (hgt-th)/2
		draw.Draw(img, image.Rect(ox-16, oy-16, ox+tw+16, oy+th+16),
			image.NewUniform(color.RGBA{R: 0, G: 0, B: 0, A: 70}), image.Point{}, draw.Over)
		for y := 0; y < th; y++ {
			for x := 0; x < tw; x++ {
				sx := b.Min.X + x*sw/tw
				sy := b.Min.Y + y*sh/th
				img.Set(ox+x, oy+y, logo.At(sx, sy))
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		return "", "", err
	}
	return rel, imageTag("default:" + lib.ID), nil
}

func decodePNG(raw []byte) (image.Image, error) {
	return png.Decode(bytes.NewReader(raw))
}

func (h *Handler) cacheLibraryImage(libID, srcRel, dstRel string) error {
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return err
	}
	src, ok := h.resolveItemImagePath(srcRel)
	if !ok {
		return os.ErrNotExist
	}
	dst := filepath.Join(absData, filepath.FromSlash(dstRel))
	if !strings.HasPrefix(dst, absData+string(os.PathSeparator)) {
		return os.ErrPermission
	}
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".lib-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, io.LimitReader(in, 20<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}

	// 清掉这个库的旧封面
	entries, _ := os.ReadDir(filepath.Dir(dst))
	prefix := libID + "-"
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || e.Name() == filepath.Base(dst) {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(dst), e.Name()))
	}
	return nil
}

// serveLibraryImage 处理 /Items/{libraryId}/Images/{type}。
func (h *Handler) serveLibraryImage(w http.ResponseWriter, r *http.Request, lib *store.Library) {
	if !h.imageAccessAllowed(r, lib.ID) {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	primaryRel, _, backdropRel, _ := h.libraryArtwork(lib)
	rel := ""
	switch strings.ToLower(r.PathValue("imageType")) {
	case "primary":
		rel = primaryRel
	case "backdrop":
		rel = backdropRel
	}
	if rel == "" {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}

	// backdrop 可能来自条目自己的本地图片（local: 前缀），必须走统一解析，
	// 直接拼 DataDir 会变成 /data/local:/...，客户端拿到 404 就整张卡片空白。
	full, ok := h.resolveItemImagePath(rel)
	if !ok {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeFile(w, r, full)
}

// validArtworkRel 确认图片真实可读，避免返回一个客户端必然拿到 404 的 tag。
func (h *Handler) validArtworkRel(rel string) string {
	if rel == "" {
		return ""
	}
	if _, ok := h.resolveItemImagePath(rel); ok {
		return rel
	}
	return ""
}
