package api

import (
	"bytes"
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"

	"lime/internal/config"
	"lime/internal/store"
)

// 库封面：默认「斜角海报墙」——取库内最新 9 张海报排成三列并整体旋转，
// 左侧留标题区；精简版「简约」只取最新一张做模糊背景。
// 尺寸与 lime-cover 一致，客户端库里按横版显示。
const (
	coverW       = 1920
	coverH       = 1080
	coverVersion = "cover-v5"
)

// coverTitle 是库封面的标题配置，抄自 lime-cover 的 title_config：
// 每个库一段「中文标题 / 英文副标题 / 背景色」。
type coverTitle struct {
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Color    string `json:"color,omitempty"`
}

// defaultCoverTitles 对应 lime-cover 的默认标题配置，库名按 LiMe 的实际命名。
var defaultCoverTitles = map[string]coverTitle{
	"国产电影": {"", "MOVIES", "#3E2E2E"},
	"日韩电影": {"", "MOVIES", "#2E344A"},
	"动画电影": {"", "MOVIES", "#2E3A46"},
	"欧美电影": {"", "MOVIES", "#2E344A"},
	"亚太电影": {"", "MOVIES", "#3A2E3E"},
	"国产剧":  {"", "TV SHOWS", "#2E4A34"},
	"日韩剧":  {"", "TV SHOWS", "#4A2E3E"},
	"欧美剧":  {"", "TV SHOWS", "#2E3A4A"},
	"亚太剧集": {"", "TV SHOWS", "#3A2E4A"},
	"日漫":   {"", "ANIME", "#2E4A4A"},
	"国漫":   {"", "ANIME", "#3A2E4A"},
	"美漫":   {"", "ANIME", "#2F3E55"},
	"综艺":   {"", "VARIETY", "#4A3A2E"},
	"纪录片":  {"", "DOCUMENTARY", "#3A3F44"},
	"儿童节目": {"", "KIDS", "#4A3E2E"},
	"演唱会":  {"", "CONCERTS", "#2E4A46"},
}

// coverTitles 返回默认配置叠加 settings.library_cover_titles 后的结果。
func (h *Handler) coverTitles() map[string]coverTitle {
	out := make(map[string]coverTitle, len(defaultCoverTitles))
	for k, v := range defaultCoverTitles {
		out[k] = v
	}
	raw, err := h.db.GetSetting("library_cover_titles")
	if err != nil || strings.TrimSpace(raw) == "" {
		return out
	}
	var custom map[string]coverTitle
	if json.Unmarshal([]byte(raw), &custom) != nil {
		return out
	}
	for name, v := range custom {
		base := out[name]
		if v.Title == "" {
			v.Title = base.Title
		}
		if v.Subtitle == "" {
			v.Subtitle = base.Subtitle
		}
		if v.Color == "" {
			v.Color = base.Color
		}
		out[name] = v
	}
	return out
}

// coverTitleFor 按库名取配置；「亚太剧集」和配置里的「亚太剧」也能对上。
func (h *Handler) coverTitleFor(lib *store.Library) coverTitle {
	all := h.coverTitles()
	if t, ok := all[lib.Name]; ok {
		if t.Title == "" {
			t.Title = lib.Name
		}
		if t.Subtitle == "" {
			t.Subtitle = coverSubtitle(lib.Kind)
		}
		return t
	}
	for name, t := range all {
		if strings.Contains(lib.Name, name) || strings.Contains(name, lib.Name) {
			if t.Title == "" {
				t.Title = lib.Name
			}
			if t.Subtitle == "" {
				t.Subtitle = coverSubtitle(lib.Kind)
			}
			return t
		}
	}
	return coverTitle{Title: lib.Name, Subtitle: coverSubtitle(lib.Kind)}
}

func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}

// coverSource 是参与封面的一个条目及其可读图片路径。
type coverSource struct {
	item *store.Item
	full string
}

//go:embed assets/fonts/NotoSansSC-Black-gb.ttf
var coverFontData []byte

var (
	coverFontOnce sync.Once
	coverFontVal  *opentype.Font
	coverFontErr  error
)

func coverFont() (*opentype.Font, error) {
	coverFontOnce.Do(func() {
		coverFontVal, coverFontErr = opentype.Parse(coverFontData)
	})
	return coverFontVal, coverFontErr
}

// libraryCoverStyle 读库封面样式，默认 collage（斜角海报墙）。
// minimal=单张模糊，latest=回退到库内最新背景图（无背景图时用海报）。
func (h *Handler) libraryCoverStyle() string {
	v, err := h.db.GetSetting("library_cover_style")
	if err != nil {
		return "collage"
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "latest":
		return "latest"
	case "minimal":
		return "minimal"
	default:
		return "collage"
	}
}

// libraryCoverPoster 按当前样式生成/复用封面；latest 样式返回空，由旧逻辑处理。
func (h *Handler) libraryCoverPoster(lib *store.Library) (rel, tag string, item *store.Item, err error) {
	switch h.libraryCoverStyle() {
	case "latest":
		return "", "", nil, nil
	case "minimal":
		return h.minimalLibraryPoster(lib)
	default:
		return h.collageLibraryPoster(lib)
	}
}

// NewCoverGenerator 构造一个只用于封面生成的轻量 Handler（不需要 HTTP 路由）。
func NewCoverGenerator(cfg config.Config, db *store.DB, log *slog.Logger) *Handler {
	return &Handler{cfg: cfg, db: db, log: log}
}

// PreGenerateCovers 给所有库预生成封面，供每日夜间任务调用。
// 生成失败只记日志，不影响其他库。
func (h *Handler) PreGenerateCovers() {
	libs, err := h.db.ListLibraries()
	if err != nil {
		h.log.Warn("pre-generate covers: list libraries failed", "err", err)
		return
	}
	for _, lib := range libs {
		if h.libraryCoverStyle() == "latest" {
			return
		}
		start := time.Now()
		rel, _, _, err := h.libraryCoverPoster(lib)
		if err != nil {
			h.log.Warn("pre-generate cover failed", "library", lib.Name, "err", err)
			continue
		}
		if rel != "" {
			h.log.Info("library cover pre-generated", "library", lib.Name, "ms", time.Since(start).Milliseconds())
		}
	}
}

// minimalLibraryPoster 生成/复用简约库封面，返回相对 data 的路径和 tag。
func (h *Handler) minimalLibraryPoster(lib *store.Library) (rel, tag string, item *store.Item, err error) {
	h.coverMu.Lock()
	defer h.coverMu.Unlock()

	items, err := h.db.LibraryPosterCandidates(lib.ID, 1)
	if err != nil {
		return "", "", nil, err
	}
	if len(items) == 0 || items[0].ImagePath == "" {
		return "", "", nil, nil
	}
	it := items[0]
	full, ok := h.resolveItemImagePath(it.ImagePath)
	if !ok {
		return "", "", it, nil
	}

	sig := coverSignature(lib, it, full)
	rel = filepath.ToSlash(filepath.Join("library-images", lib.ID+"-minimal-"+sig[:12]+".jpg"))
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return "", "", it, err
	}
	dst := filepath.Join(absData, filepath.FromSlash(rel))
	if st, statErr := os.Stat(dst); statErr == nil && st.Size() > 0 {
		return rel, sig[:12], it, nil
	}

	start := time.Now()
	src, err := decodeCoverSource(full)
	if err != nil {
		return "", "", it, err
	}
	canvas, err := renderMinimalCover(src, lib.Name, coverSubtitle(lib.Kind))
	if err != nil {
		return "", "", it, err
	}
	if err := writeCoverJPEG(dst, canvas); err != nil {
		return "", "", it, err
	}
	cleanupOldCovers(filepath.Dir(dst), lib.ID, filepath.Base(dst))
	h.log.Info("library cover generated", "library", lib.Name, "source", it.Name, "ms", time.Since(start).Milliseconds())
	return rel, sig[:12], it, nil
}

// collageLibraryPoster 生成/复用「斜角海报墙」封面：最新 9 张海报排三列，
// 整体旋转后压在右侧。新条目入库会改变签名，下次请求自动重建。
func (h *Handler) collageLibraryPoster(lib *store.Library) (rel, tag string, item *store.Item, err error) {
	h.coverMu.Lock()
	defer h.coverMu.Unlock()

	items, err := h.db.LibraryPosterCandidates(lib.ID, 9)
	if err != nil {
		return "", "", nil, err
	}
	sources := make([]coverSource, 0, 9)
	for _, it := range items {
		if it.ImagePath == "" {
			continue
		}
		if full, ok := h.resolveItemImagePath(it.ImagePath); ok {
			sources = append(sources, coverSource{item: it, full: full})
		}
	}
	if len(sources) == 0 {
		return "", "", nil, nil
	}
	first := sources[0].item
	meta := h.coverTitleFor(lib)
	if meta.Title == "" {
		meta.Title = lib.Name
	}

	sig := collageSignature(lib, meta, sources)
	rel = filepath.ToSlash(filepath.Join("library-images", lib.ID+"-collage-"+sig[:12]+".jpg"))
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return "", "", first, err
	}
	dst := filepath.Join(absData, filepath.FromSlash(rel))
	if st, statErr := os.Stat(dst); statErr == nil && st.Size() > 0 {
		return rel, sig[:12], first, nil
	}

	start := time.Now()
	posters := make([]image.Image, 0, len(sources))
	for _, s := range sources {
		img, derr := decodeCoverSource(s.full)
		if derr != nil {
			h.log.Warn("cover source decode failed", "item", s.item.Name, "err", derr)
			continue
		}
		posters = append(posters, img)
	}
	if len(posters) == 0 {
		return "", "", first, fmt.Errorf("no decodable posters")
	}
	var bg *color.RGBA
	if c, ok := parseHexColor(meta.Color); ok {
		bg = &c
	}
	canvas, err := renderCollageCover(posters, meta.Title, meta.Subtitle, bg)
	if err != nil {
		return "", "", first, err
	}
	if err := writeCoverJPEG(dst, canvas); err != nil {
		return "", "", first, err
	}
	cleanupOldCovers(filepath.Dir(dst), lib.ID, filepath.Base(dst))
	h.log.Info("library cover generated",
		"library", lib.Name, "style", "collage", "posters", len(posters),
		"ms", time.Since(start).Milliseconds())
	return rel, sig[:12], first, nil
}

// collageSignature 覆盖所有参与拼贴的条目和图片文件，任一变化都会重建。
// 标题配置和背景色也算进去，改配色后不用手动删缓存。
func collageSignature(lib *store.Library, meta coverTitle, sources []coverSource) string {
	var b strings.Builder
	b.WriteString(coverVersion)
	b.WriteString("|collage|")
	b.WriteString(lib.ID)
	b.WriteString("|")
	b.WriteString(lib.Name)
	b.WriteString("|")
	b.WriteString(lib.Kind)
	b.WriteString("|")
	b.WriteString(meta.Title)
	b.WriteString("|")
	b.WriteString(meta.Subtitle)
	b.WriteString("|")
	b.WriteString(meta.Color)
	for _, s := range sources {
		b.WriteString("|")
		b.WriteString(s.item.ID)
		b.WriteString("|")
		b.WriteString(s.item.ImagePath)
		if st, err := os.Stat(s.full); err == nil {
			b.WriteString(fmt.Sprintf("|%d|%d", st.ModTime().UnixNano(), st.Size()))
		}
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// renderCollageCover 背景 + 模糊海报纹理 + 斜角三列海报墙 + 标题。
// bg 非空时用库配置的背景色（抄自 lime-cover 的 title_config），
// 空则退化为从海报提取主色。
func renderCollageCover(posters []image.Image, title, subtitle string, bg *color.RGBA) (*image.RGBA, error) {
	const sw, sh = coverW / 4, coverH / 4
	small := image.NewRGBA(image.Rect(0, 0, sw, sh))
	if err := drawCoverInto(small, posters[0]); err != nil {
		return nil, err
	}
	boxBlur(small, 6, 2)

	dst := image.NewRGBA(image.Rect(0, 0, coverW, coverH))
	if bg != nil {
		fillConfiguredGradient(dst, *bg)
		overlayAlpha(dst, small, 0.12)
	} else {
		tint := vibrantTint(small)
		fillDiagonalGradient(dst, tint)
		overlayAlpha(dst, small, 0.20)
	}
	addLeftShade(dst)
	drawPosterWall(dst, posters)
	if err := drawCoverTitle(dst, title, subtitle, 120, 620); err != nil {
		return nil, err
	}
	return dst, nil
}

// fillConfiguredGradient 用配置的背景色做左上亮、右下暗的柔和渐变。
// 配置色本身偏暗（lime-cover 的风格），这里抬一点亮度，避免整张发黑。
func fillConfiguredGradient(dst *image.RGBA, base color.RGBA) {
	h, s, v := rgbToHSV(base.R, base.G, base.B)
	r1, g1, b1 := hsvToRGB(h, math.Min(1, s*0.92+0.02), math.Min(1, v*1.55+0.07))
	r2, g2, b2 := hsvToRGB(h, math.Min(1, s*1.06), math.Max(0.05, v*0.86))
	w, hh := dst.Bounds().Dx(), dst.Bounds().Dy()
	for y := 0; y < hh; y++ {
		for x := 0; x < w; x++ {
			t := float64(x)/float64(w)*0.62 + float64(y)/float64(hh)*0.38
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(float64(r1)*(1-t) + float64(r2)*t)
			dst.Pix[i+1] = uint8(float64(g1)*(1-t) + float64(g2)*t)
			dst.Pix[i+2] = uint8(float64(b1)*(1-t) + float64(b2)*t)
			dst.Pix[i+3] = 255
		}
	}
}

// fillDiagonalGradient 用主色做左上亮、右下暗的对角渐变。
func fillDiagonalGradient(dst *image.RGBA, tint color.RGBA) {
	h, s, v := rgbToHSV(tint.R, tint.G, tint.B)
	r1, g1, b1 := hsvToRGB(h, math.Min(1, s*1.6+0.08), math.Min(0.66, v*2.4+0.10))
	r2, g2, b2 := hsvToRGB(h, math.Min(1, s*1.2+0.05), math.Max(0.05, v*0.70))
	w, hh := dst.Bounds().Dx(), dst.Bounds().Dy()
	for y := 0; y < hh; y++ {
		for x := 0; x < w; x++ {
			t := float64(x)/float64(w)*0.65 + float64(y)/float64(hh)*0.35
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(float64(r1)*(1-t) + float64(r2)*t)
			dst.Pix[i+1] = uint8(float64(g1)*(1-t) + float64(g2)*t)
			dst.Pix[i+2] = uint8(float64(b1)*(1-t) + float64(b2)*t)
			dst.Pix[i+3] = 255
		}
	}
}

// overlayAlpha 把模糊小图放大后以 alpha 叠加，给纯色背景一点海报纹理。
func overlayAlpha(dst *image.RGBA, small *image.RGBA, alpha float64) {
	layer := image.NewRGBA(dst.Bounds())
	draw.ApproxBiLinear.Scale(layer, layer.Bounds(), small, small.Bounds(), draw.Src, nil)
	mask := image.NewUniform(color.Alpha{A: uint8(math.Round(alpha * 255))})
	stddraw.DrawMask(dst, dst.Bounds(), layer, image.Point{}, mask, image.Point{}, stddraw.Over)
}

// drawPosterWall 三列海报墙：卡片放大、列距收紧，每列上下错位，
// 整面墙绕自身中心轻微旋转后压在画面右侧，四边都溢出画面，避免出现空角。
func drawPosterWall(dst *image.RGBA, posters []image.Image) {
	const (
		cardW = 300
		cardH = 450
		cols  = 3
		rows  = 3
		gap   = 24.0
		padX  = 40
		padY  = 140
		// 墙面中心落在画面右侧，左边缘让出标题区。
		centerX = 1395.0
		centerY = 540.0
		angle   = -9.5 * math.Pi / 180
	)
	wallW := cols*cardW + int(float64(cols-1)*gap)
	wallH := rows*cardH + int(float64(rows-1)*gap)
	wall := image.NewRGBA(image.Rect(0, 0, wallW+padX*2, wallH+padY*2))

	colShift := []float64{-70, 40, -40}
	for c := 0; c < cols; c++ {
		for r := 0; r < rows; r++ {
			card := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
			if err := drawCoverInto(card, posters[(c*rows+r)%len(posters)]); err != nil {
				continue
			}
			roundCorners(card, 18)
			x := padX + c*(cardW+int(gap))
			y := padY + int(colShift[c]) + r*(cardH+int(gap))
			stddraw.Draw(wall, image.Rect(x, y, x+cardW, y+cardH), card, image.Point{}, stddraw.Over)
		}
	}

	// srcCX/srcCY 与旋转中心取同一点：整张墙绕自身中心转，再摆到画面右侧。
	drawRotated(dst, wall, centerX, centerY, centerX, centerY, angle)
}

// drawRotated 把 src 以 (srcCX,srcCY) 为中心、绕 (rotCX,rotCY) 旋转后贴到 dst。
func drawRotated(dst, src *image.RGBA, srcCX, srcCY, rotCX, rotCY, angle float64) {
	cosA, sinA := math.Cos(angle), math.Sin(angle)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()

	px, py := srcCX-rotCX, srcCY-rotCY
	ccx := rotCX + px*cosA - py*sinA
	ccy := rotCY + px*sinA + py*cosA

	rad := math.Hypot(float64(w), float64(h))/2 + 1
	x0, y0 := int(ccx-rad), int(ccy-rad)
	x1, y1 := int(ccx+rad)+1, int(ccy+rad)+1
	db := dst.Bounds()
	if x0 < db.Min.X {
		x0 = db.Min.X
	}
	if y0 < db.Min.Y {
		y0 = db.Min.Y
	}
	if x1 > db.Max.X {
		x1 = db.Max.X
	}
	if y1 > db.Max.Y {
		y1 = db.Max.Y
	}

	halfW, halfH := float64(w)/2, float64(h)/2
	for y := y0; y < y1; y++ {
		dy := float64(y) + 0.5 - ccy
		for x := x0; x < x1; x++ {
			dx := float64(x) + 0.5 - ccx
			sx := dx*cosA + dy*sinA + halfW
			sy := -dx*sinA + dy*cosA + halfH
			if sx < 0 || sy < 0 || sx >= float64(w) || sy >= float64(h) {
				continue
			}
			r, g, b, a := sampleBilinear(src, sx, sy)
			if a == 0 {
				continue
			}
			sa := float64(a) / 255
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(float64(r)*sa + float64(dst.Pix[i])*(1-sa))
			dst.Pix[i+1] = uint8(float64(g)*sa + float64(dst.Pix[i+1])*(1-sa))
			dst.Pix[i+2] = uint8(float64(b)*sa + float64(dst.Pix[i+2])*(1-sa))
		}
	}
}

func sampleBilinear(img *image.RGBA, x, y float64) (uint8, uint8, uint8, uint8) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	x0, y0 := int(x), int(y)
	if x0 >= w-1 {
		x0 = w - 2
	}
	if y0 >= h-1 {
		y0 = h - 2
	}
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	fx, fy := x-float64(x0), y-float64(y0)
	p := img.Pix
	stride := img.Stride
	i00 := img.PixOffset(x0, y0)
	i10 := i00 + 4
	i01 := i00 + stride
	i11 := i01 + 4
	var out [4]uint8
	for c := 0; c < 4; c++ {
		v00 := float64(p[i00+c])
		v10 := float64(p[i10+c])
		v01 := float64(p[i01+c])
		v11 := float64(p[i11+c])
		top := v00 + (v10-v00)*fx
		bot := v01 + (v11-v01)*fx
		out[c] = uint8(math.Round(top + (bot-top)*fy))
	}
	return out[0], out[1], out[2], out[3]
}

// roundCorners 把卡片四角裁成圆角（透明），旋转时边界更柔和。
func roundCorners(img *image.RGBA, radius int) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	type corner struct{ cx, cy, x0, y0, x1, y1 int }
	corners := []corner{
		{radius, radius, 0, 0, radius, radius},
		{w - radius - 1, radius, w - radius, 0, w, radius},
		{radius, h - radius - 1, 0, h - radius, radius, h},
		{w - radius - 1, h - radius - 1, w - radius, h - radius, w, h},
	}
	rr := float64(radius * radius)
	for _, c := range corners {
		for y := c.y0; y < c.y1; y++ {
			for x := c.x0; x < c.x1; x++ {
				dx, dy := float64(x-c.cx), float64(y-c.cy)
				if dx*dx+dy*dy > rr {
					img.Pix[img.PixOffset(x, y)+3] = 0
				}
			}
		}
	}
}

// coverSignature 把会影响成图的输入都算进去：库名/类型、条目、图片文件 mtime。
func coverSignature(lib *store.Library, it *store.Item, full string) string {
	key := strings.Join([]string{
		coverVersion, lib.ID, lib.Name, lib.Kind, it.ID, it.ImagePath,
	}, "|")
	if st, err := os.Stat(full); err == nil {
		key += fmt.Sprintf("|%d|%d", st.ModTime().UnixNano(), st.Size())
	}
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])
}

// decodeCoverSource 打开本地海报并解码。webp/gif 都支持，gif 取第一帧。
func decodeCoverSource(full string) (image.Image, error) {
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 || st.Size() > 30<<20 {
		return nil, fmt.Errorf("cover source size %d out of range", st.Size())
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// renderMinimalCover 在 1/4 分辨率上做模糊和调色，再放大到 1080p，速度快很多。
// 标题居中：中文在上、英文在下，不加左侧竖条。
func renderMinimalCover(src image.Image, title, subtitle string) (*image.RGBA, error) {
	const sw, sh = coverW / 4, coverH / 4
	small := image.NewRGBA(image.Rect(0, 0, sw, sh))
	if err := drawCoverInto(small, src); err != nil {
		return nil, err
	}
	boxBlur(small, 7, 2)
	tint := vibrantTint(small)
	blendTint(small, tint, 0.84)

	dst := image.NewRGBA(image.Rect(0, 0, coverW, coverH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), small, small.Bounds(), draw.Src, nil)
	addVignette(dst, 0.55)
	if err := drawCoverTitleCentered(dst, title, subtitle); err != nil {
		return nil, err
	}
	return dst, nil
}

// drawCoverInto 等比铺满并居中裁切。
func drawCoverInto(dst *image.RGBA, src image.Image) error {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return fmt.Errorf("empty cover source")
	}
	db := dst.Bounds()
	dw, dh := db.Dx(), db.Dy()
	scale := math.Max(float64(dw)/float64(sw), float64(dh)/float64(sh))
	cw := int(math.Round(float64(dw) / scale))
	ch := int(math.Round(float64(dh) / scale))
	if cw < 1 {
		cw = 1
	}
	if ch < 1 {
		ch = 1
	}
	if cw > sw {
		cw = sw
	}
	if ch > sh {
		ch = sh
	}
	sx := sb.Min.X + (sw-cw)/2
	sy := sb.Min.Y + (sh-ch)/2
	draw.ApproxBiLinear.Scale(dst, db, src, image.Rect(sx, sy, sx+cw, sy+ch), draw.Src, nil)
	return nil
}

// vibrantTint 按饱和度加权平均取主色，避开海报的黑边和字幕。
func vibrantTint(img *image.RGBA) color.RGBA {
	b := img.Bounds()
	var r, g, bl, weight float64
	for y := b.Min.Y; y < b.Max.Y; y += 3 {
		for x := b.Min.X; x < b.Max.X; x += 3 {
			c := img.RGBAAt(x, y)
			_, s, v := rgbToHSV(c.R, c.G, c.B)
			w := 0.15 + s
			if v < 0.08 {
				w *= 0.2
			}
			r += float64(c.R) * w
			g += float64(c.G) * w
			bl += float64(c.B) * w
			weight += w
		}
	}
	if weight == 0 {
		return color.RGBA{R: 40, G: 36, B: 44, A: 255}
	}
	tint := color.RGBA{R: uint8(r / weight), G: uint8(g / weight), B: uint8(bl / weight), A: 255}
	h, s, v := rgbToHSV(tint.R, tint.G, tint.B)
	s = math.Min(1, s*1.2+0.03)
	v = math.Max(0.08, v*0.34)
	cr, cg, cb := hsvToRGB(h, s, v)
	return color.RGBA{R: cr, G: cg, B: cb, A: 255}
}

// blendTint 把模糊背景压向主色，ratio 越大越暗越纯。
func blendTint(img *image.RGBA, tint color.RGBA, ratio float64) {
	if ratio <= 0 {
		return
	}
	if ratio > 1 {
		ratio = 1
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(float64(img.Pix[i])*(1-ratio) + float64(tint.R)*ratio)
			img.Pix[i+1] = uint8(float64(img.Pix[i+1])*(1-ratio) + float64(tint.G)*ratio)
			img.Pix[i+2] = uint8(float64(img.Pix[i+2])*(1-ratio) + float64(tint.B)*ratio)
		}
	}
}

// addLeftShade 左侧压暗，保证白色标题在任何海报上都看得清。
func addLeftShade(img *image.RGBA) {
	b := img.Bounds()
	fade := float64(coverW) * 0.58
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := 0; x < int(fade); x++ {
			a := 0.56 * (1 - float64(x)/fade)
			if a <= 0 {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(float64(img.Pix[i]) * (1 - a))
			img.Pix[i+1] = uint8(float64(img.Pix[i+1]) * (1 - a))
			img.Pix[i+2] = uint8(float64(img.Pix[i+2]) * (1 - a))
		}
	}
}

func coverSubtitle(kind string) string {
	switch strings.ToLower(kind) {
	case "movies":
		return "MOVIES"
	case "tvshows":
		return "TV SHOWS"
	case "mixed":
		return "MEDIA"
	default:
		return "LIBRARY"
	}
}

func newCoverFace(size float64) (font.Face, error) {
	f, err := coverFont()
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingNone,
	})
}

// addVignette 四周柔和压暗、中心保持通透，让居中标题更醒目。
func addVignette(img *image.RGBA, strength float64) {
	if strength <= 0 {
		return
	}
	b := img.Bounds()
	cx, cy := float64(coverW)/2, float64(coverH)/2
	maxD := math.Hypot(cx, cy)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) / maxD
			a := strength * d * d
			if a <= 0.002 {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(float64(img.Pix[i]) * (1 - a))
			img.Pix[i+1] = uint8(float64(img.Pix[i+1]) * (1 - a))
			img.Pix[i+2] = uint8(float64(img.Pix[i+2]) * (1 - a))
		}
	}
}

// drawCoverTitleCentered 中文标题在上、英文副标题在下，整体水平垂直居中。
func drawCoverTitleCentered(dst *image.RGBA, title, subtitle string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	maxWidth := float64(coverW) * 0.76

	titleSize := 176.0
	face, err := newCoverFace(titleSize)
	if err != nil {
		return err
	}
	width := measureSpaced(face, title, 0)
	if width > maxWidth {
		titleSize = math.Max(96, titleSize*maxWidth/width)
		if face, err = newCoverFace(titleSize); err != nil {
			return err
		}
		title = truncateSpaced(face, title, maxWidth)
		width = measureSpaced(face, title, 0)
	}

	const subSpacing = 14.0
	subSize := 68.0
	subFace, err := newCoverFace(subSize)
	if err != nil {
		return err
	}
	subWidth := measureSpaced(subFace, subtitle, subSpacing)
	if subWidth > maxWidth {
		subSize = math.Max(40, subSize*maxWidth/subWidth)
		if subFace, err = newCoverFace(subSize); err != nil {
			return err
		}
		subtitle = truncateSpaced(subFace, subtitle, maxWidth, subSpacing)
		subWidth = measureSpaced(subFace, subtitle, subSpacing)
	}

	tm := face.Metrics()
	sm := subFace.Metrics()
	titleH := float64(tm.Ascent+tm.Descent) / 64
	subH := float64(sm.Ascent+sm.Descent) / 64
	const gap = 34.0
	blockH := titleH + gap + subH
	top := (coverH - blockH) / 2
	titleBase := top + float64(tm.Ascent)/64
	subBase := top + titleH + gap + float64(sm.Ascent)/64
	titleX := (coverW - width) / 2
	subX := (coverW - subWidth) / 2

	shadow := color.RGBA{0, 0, 0, 120}
	drawSpaced(dst, face, title, titleX, titleBase+8, 0, shadow)
	drawSpaced(dst, face, title, titleX, titleBase, 0, color.RGBA{255, 255, 255, 255})
	drawSpaced(dst, subFace, subtitle, subX, subBase+6, subSpacing, shadow)
	drawSpaced(dst, subFace, subtitle, subX, subBase, subSpacing, color.RGBA{248, 248, 248, 245})
	return nil
}

func drawCoverTitle(dst *image.RGBA, title, subtitle string, x, maxWidth float64) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	titleSize := 168.0
	face, err := newCoverFace(titleSize)
	if err != nil {
		return err
	}
	width := measureSpaced(face, title, 0)
	if width > maxWidth {
		titleSize = math.Max(96, titleSize*maxWidth/width)
		if face, err = newCoverFace(titleSize); err != nil {
			return err
		}
		title = truncateSpaced(face, title, maxWidth)
	}

	subSize := 64.0
	subFace, err := newCoverFace(subSize)
	if err != nil {
		return err
	}
	subWidth := measureSpaced(subFace, subtitle, 10)
	if subWidth > maxWidth {
		subSize = math.Max(40, subSize*maxWidth/subWidth)
		if subFace, err = newCoverFace(subSize); err != nil {
			return err
		}
		subtitle = truncateSpaced(subFace, subtitle, maxWidth, 10)
	}

	tm := face.Metrics()
	sm := subFace.Metrics()
	titleH := float64(tm.Ascent+tm.Descent) / 64
	subH := float64(sm.Ascent+sm.Descent) / 64
	const gap = 26.0
	blockH := titleH + gap + subH
	top := (coverH - blockH) / 2
	titleBase := top + float64(tm.Ascent)/64
	subBase := top + titleH + gap + float64(sm.Ascent)/64

	barX := x - 46
	barH := 0.92 * titleH
	barTop := top + (titleH-barH)/2
	fillRoundRect(dst, image.Rect(int(barX), int(barTop), int(barX)+11, int(barTop+barH)), 5, color.RGBA{255, 255, 255, 235})

	// 阴影先画，再画白字，边缘更干净。
	shadow := color.RGBA{0, 0, 0, 95}
	drawSpaced(dst, face, title, x, titleBase+8, 0, shadow)
	drawSpaced(dst, face, title, x, titleBase, 0, color.RGBA{255, 255, 255, 255})
	drawSpaced(dst, subFace, subtitle, x, subBase+6, 10, color.RGBA{0, 0, 0, 95})
	drawSpaced(dst, subFace, subtitle, x, subBase, 10, color.RGBA{245, 245, 245, 240})
	return nil
}

func measureSpaced(face font.Face, s string, spacing float64) float64 {
	var w float64
	for _, r := range s {
		adv, _ := face.GlyphAdvance(r)
		w += float64(adv)/64 + spacing
	}
	if len(s) > 0 {
		w -= spacing
	}
	return w
}

func truncateSpaced(face font.Face, s string, maxWidth float64, spacing ...float64) string {
	sp := 0.0
	if len(spacing) > 0 {
		sp = spacing[0]
	}
	if measureSpaced(face, s, sp) <= maxWidth {
		return s
	}
	ellipsis := "…"
	avail := maxWidth - measureSpaced(face, ellipsis, sp)
	var out []rune
	var w float64
	for _, r := range s {
		adv, _ := face.GlyphAdvance(r)
		next := w + float64(adv)/64 + sp
		if next > avail {
			break
		}
		out = append(out, r)
		w = next
	}
	return string(out) + ellipsis
}

func drawSpaced(dst *image.RGBA, face font.Face, s string, x, baseline, spacing float64, col color.RGBA) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face}
	for _, r := range s {
		d.Dot = fixed.P(int(math.Round(x)), int(math.Round(baseline)))
		d.DrawString(string(r))
		adv, _ := face.GlyphAdvance(r)
		x += float64(adv)/64 + spacing
	}
}

func fillRoundRect(dst *image.RGBA, r image.Rectangle, radius int, col color.RGBA) {
	if radius <= 0 {
		stddraw.Draw(dst, r, image.NewUniform(col), image.Point{}, stddraw.Over)
		return
	}
	rr := float64(radius)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cx, cy := x, y
			if x < r.Min.X+radius {
				cx = r.Min.X + radius
			} else if x > r.Max.X-1-radius {
				cx = r.Max.X - 1 - radius
			}
			if y < r.Min.Y+radius {
				cy = r.Min.Y + radius
			} else if y > r.Max.Y-1-radius {
				cy = r.Max.Y - 1 - radius
			}
			dx, dy := float64(x-cx), float64(y-cy)
			if dx*dx+dy*dy > rr*rr {
				continue
			}
			blendPixel(dst, x, y, col)
		}
	}
}

func blendPixel(dst *image.RGBA, x, y int, col color.RGBA) {
	a := float64(col.A) / 255
	if a <= 0 {
		return
	}
	i := dst.PixOffset(x, y)
	dst.Pix[i] = uint8(float64(col.R)*a + float64(dst.Pix[i])*(1-a))
	dst.Pix[i+1] = uint8(float64(col.G)*a + float64(dst.Pix[i+1])*(1-a))
	dst.Pix[i+2] = uint8(float64(col.B)*a + float64(dst.Pix[i+2])*(1-a))
}

// boxBlur 用滑动窗口做 box blur，跑多遍近似高斯。
func boxBlur(img *image.RGBA, radius, passes int) {
	if radius < 1 || passes < 1 {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	stride := img.Stride
	tmp := make([]uint8, len(img.Pix))
	for p := 0; p < passes; p++ {
		blurH(img.Pix, tmp, w, h, stride, radius)
		blurV(tmp, img.Pix, w, h, stride, radius)
	}
}

func blurH(src, dst []byte, w, h, stride, r int) {
	win := 2*r + 1
	for y := 0; y < h; y++ {
		row := y * stride
		var sum [3]int
		for x := -r; x <= r; x++ {
			xx := x
			if xx < 0 {
				xx = 0
			} else if xx >= w {
				xx = w - 1
			}
			i := row + xx*4
			sum[0] += int(src[i])
			sum[1] += int(src[i+1])
			sum[2] += int(src[i+2])
		}
		for x := 0; x < w; x++ {
			o := row + x*4
			dst[o] = uint8(sum[0] / win)
			dst[o+1] = uint8(sum[1] / win)
			dst[o+2] = uint8(sum[2] / win)
			dst[o+3] = 255

			outX := x - r
			inX := x + r + 1
			if outX < 0 {
				outX = 0
			}
			if inX >= w {
				inX = w - 1
			}
			oi := row + outX*4
			ii := row + inX*4
			sum[0] += int(src[ii]) - int(src[oi])
			sum[1] += int(src[ii+1]) - int(src[oi+1])
			sum[2] += int(src[ii+2]) - int(src[oi+2])
		}
	}
}

func blurV(src, dst []byte, w, h, stride, r int) {
	win := 2*r + 1
	for x := 0; x < w; x++ {
		var sum [3]int
		for y := -r; y <= r; y++ {
			yy := y
			if yy < 0 {
				yy = 0
			} else if yy >= h {
				yy = h - 1
			}
			i := yy*stride + x*4
			sum[0] += int(src[i])
			sum[1] += int(src[i+1])
			sum[2] += int(src[i+2])
		}
		for y := 0; y < h; y++ {
			o := y*stride + x*4
			dst[o] = uint8(sum[0] / win)
			dst[o+1] = uint8(sum[1] / win)
			dst[o+2] = uint8(sum[2] / win)
			dst[o+3] = 255

			outY := y - r
			inY := y + r + 1
			if outY < 0 {
				outY = 0
			}
			if inY >= h {
				inY = h - 1
			}
			oi := outY*stride + x*4
			ii := inY*stride + x*4
			sum[0] += int(src[ii]) - int(src[oi])
			sum[1] += int(src[ii+1]) - int(src[oi+1])
			sum[2] += int(src[ii+2]) - int(src[oi+2])
		}
	}
}

func writeCoverJPEG(dst string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 86}); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cover-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// cleanupOldCovers 只清理同一库的历史简约封面，custom 上传的图不能被误删。
func cleanupOldCovers(dir, libID, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefixes := []string{libID + "-minimal-", libID + "-collage-"}
	for _, e := range entries {
		if e.IsDir() || e.Name() == keep {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(e.Name(), prefix) {
				_ = os.Remove(filepath.Join(dir, e.Name()))
				break
			}
		}
	}
}

// rgbToHSV / hsvToRGB 标准库没有，自己换算，h/s/v 都是 0..1。
func rgbToHSV(r, g, b uint8) (h, s, v float64) {
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	maxv := math.Max(rf, math.Max(gf, bf))
	minv := math.Min(rf, math.Min(gf, bf))
	v = maxv
	d := maxv - minv
	if maxv == 0 {
		return 0, 0, 0
	}
	s = d / maxv
	if d == 0 {
		return 0, 0, v
	}
	switch maxv {
	case rf:
		h = (gf - bf) / d
		if gf < bf {
			h += 6
		}
	case gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h /= 6
	return h, s, v
}

func hsvToRGB(h, s, v float64) (uint8, uint8, uint8) {
	if s <= 0 {
		c := uint8(v*255 + 0.5)
		return c, c, c
	}
	h = math.Mod(h, 1)
	if h < 0 {
		h++
	}
	i := int(h*6) % 6
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)
	var r, g, b float64
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return uint8(r*255 + 0.5), uint8(g*255 + 0.5), uint8(b*255 + 0.5)
}
