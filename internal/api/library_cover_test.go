package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

// 预览渲染效果：COVER_PREVIEW_IN 指向海报目录，COVER_PREVIEW_OUT 指定输出目录。
func TestRenderMinimalCoverPreview(t *testing.T) {
	in := os.Getenv("COVER_PREVIEW_IN")
	out := os.Getenv("COVER_PREVIEW_OUT")
	if in == "" || out == "" {
		t.Skip("set COVER_PREVIEW_IN and COVER_PREVIEW_OUT")
	}
	entries, err := os.ReadDir(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	titles := [][2]string{{"电影", "MOVIES"}, {"飞牛影视", "MEDIA LIBRARY"}, {"艺术", "MOVIES"}}
	var all []image.Image
	for _, e := range entries {
		f, err := os.Open(filepath.Join(in, e.Name()))
		if err != nil {
			continue
		}
		src, _, derr := image.Decode(f)
		_ = f.Close()
		if derr == nil {
			all = append(all, src)
		}
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(in, e.Name()))
		if err != nil {
			continue
		}
		src, _, derr := image.Decode(f)
		_ = f.Close()
		if derr != nil {
			t.Logf("skip %s: %v", e.Name(), derr)
			continue
		}
		for _, ti := range titles {
			canvas, err := renderMinimalCover(src, ti[0], ti[1])
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(out, ti[0]+"-"+e.Name())
			of, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := jpeg.Encode(of, canvas, &jpeg.Options{Quality: 88}); err != nil {
				_ = of.Close()
				t.Fatal(err)
			}
			_ = of.Close()
			n++
			if len(all) > 0 {
				col, err := renderCollageCover(all, ti[0], ti[1], nil)
				if err != nil {
					t.Fatal(err)
				}
				cf, err := os.Create(filepath.Join(out, "collage-"+ti[0]+".jpg"))
				if err != nil {
					t.Fatal(err)
				}
				if err := jpeg.Encode(cf, col, &jpeg.Options{Quality: 88}); err != nil {
					_ = cf.Close()
					t.Fatal(err)
				}
				_ = cf.Close()
			}
		}
	}
	t.Logf("rendered %d previews to %s", n, out)
}

func TestRenderMinimalCover(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 800; x++ {
			src.SetRGBA(x, y, color.RGBA{
				R: uint8(30 + x*180/800),
				G: uint8(60 + y*120/1200),
				B: uint8(120 + (x+y)%80),
				A: 255,
			})
		}
	}
	canvas, err := renderMinimalCover(src, "电影", "MOVIES")
	if err != nil {
		t.Fatal(err)
	}
	if got := canvas.Bounds(); got != image.Rect(0, 0, coverW, coverH) {
		t.Fatalf("size = %v", got)
	}

	// 标题区域应有接近白色的字形像素。
	bright := 0
	for y := 300; y < 780; y++ {
		for x := 100; x < 1200; x++ {
			c := canvas.RGBAAt(x, y)
			if c.R > 220 && c.G > 220 && c.B > 220 {
				bright++
			}
		}
	}
	if bright < 500 {
		t.Fatalf("title pixels = %d, expected white text", bright)
	}
}

func TestCoverSubtitle(t *testing.T) {
	cases := map[string]string{
		"movies": "MOVIES", "tvshows": "TV SHOWS", "mixed": "MEDIA", "": "LIBRARY",
	}
	for kind, want := range cases {
		if got := coverSubtitle(kind); got != want {
			t.Fatalf("coverSubtitle(%q) = %q, want %q", kind, got, want)
		}
	}
}

func writeRealCoverJPEG(t *testing.T, dir, rel string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 800; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(20 + x*200/800),
				G: uint8(40 + y*100/1200),
				B: uint8(90 + (x*y)%120),
				A: 255,
			})
		}
	}
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestLibraryMinimalCoverGenerated(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.SetSetting("library_cover_style", "minimal"); err != nil {
		t.Fatal(err)
	}
	writeRealCoverJPEG(t, f.env.cfg.DataDir, "images/cover-real.jpg")
	it := &store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "测试电影", SortName: "测试电影", ImagePath: "images/cover-real.jpg",
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Users/"+f.admin.ID+"/Views", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("views = %d: %s", rr.Code, rr.Body.String())
	}
	var views dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 1 {
		t.Fatalf("views = %d", len(views.Items))
	}
	tag := views.Items[0].ImageTags["Primary"]
	if tag == "" {
		t.Fatalf("missing primary tag: %+v", views.Items[0])
	}
	if views.Items[0].PrimaryImageItemId != lib.ID {
		t.Fatalf("PrimaryImageItemId = %q, want %q", views.Items[0].PrimaryImageItemId, lib.ID)
	}

	rr = f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary?tag="+tag, nil, "")
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("cover = %d, %d bytes", rr.Code, rr.Body.Len())
	}
	img, _, err := image.Decode(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds(); got.Dx() != coverW || got.Dy() != coverH {
		t.Fatalf("cover size = %v, want %dx%d", got, coverW, coverH)
	}

	// 第二次请求应命中缓存，不再重写文件。
	cachePath := filepath.Join(f.env.cfg.DataDir, "library-images", lib.ID+"-minimal-"+tag+".jpg")
	st1, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	rr = f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary?tag="+tag, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("second cover = %d", rr.Code)
	}
	st2, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("cover regenerated instead of cache hit")
	}
}

func TestLibraryCoverStyleLatest(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.SetSetting("library_cover_style", "latest"); err != nil {
		t.Fatal(err)
	}
	writeRealCoverJPEG(t, f.env.cfg.DataDir, "images/cover-latest.jpg")
	it := &store.Item{
		ID: "m2", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "测试电影", SortName: "测试电影", ImagePath: "images/cover-latest.jpg",
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("cover = %d: %s", rr.Code, rr.Body.String())
	}
	img, _, err := image.Decode(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() == coverW && img.Bounds().Dy() == coverH {
		t.Fatal("latest style should not generate the 1920x1080 cover")
	}
}

func TestLibraryBackdropLocalPath(t *testing.T) {
	f := newUserFixture(t)
	mediaDir := t.TempDir()
	lib, err := f.env.db.UpsertLibrary("艺术", mediaDir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	// 回退到最新单张模式，验证 local: backdrop 的解析。
	if err := f.env.db.SetSetting("library_cover_style", "latest"); err != nil {
		t.Fatal(err)
	}
	poster := filepath.Join(mediaDir, "poster.jpg")
	fanart := filepath.Join(mediaDir, "fanart.jpg")
	writeRealCoverJPEG(t, mediaDir, "poster.jpg")
	writeRealCoverJPEG(t, mediaDir, "fanart.jpg")

	it := &store.Item{
		ID: "local1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "本地电影", SortName: "本地电影",
		ImagePath:    "local:" + filepath.ToSlash(poster),
		BackdropPath: "local:" + filepath.ToSlash(fanart),
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Backdrop", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("backdrop = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/jpeg") {
		t.Fatalf("backdrop content type = %q", ct)
	}

	rr = f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("primary = %d", rr.Code)
	}
}

func TestRenderCollageCover(t *testing.T) {
	posters := make([]image.Image, 9)
	for i := range posters {
		img := image.NewRGBA(image.Rect(0, 0, 400, 600))
		for y := 0; y < 600; y++ {
			for x := 0; x < 400; x++ {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(30 + i*20), G: uint8(80 + x*80/400), B: uint8(120 + y*80/600), A: 255,
				})
			}
		}
		posters[i] = img
	}
	canvas, err := renderCollageCover(posters, "电影", "MOVIES", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := canvas.Bounds(); got != image.Rect(0, 0, coverW, coverH) {
		t.Fatalf("size = %v", got)
	}
	bright := 0
	for y := 300; y < 780; y++ {
		for x := 100; x < 800; x++ {
			c := canvas.RGBAAt(x, y)
			if c.R > 220 && c.G > 220 && c.B > 220 {
				bright++
			}
		}
	}
	if bright < 500 {
		t.Fatalf("title pixels = %d", bright)
	}
}

func TestLibraryCollageCoverGenerated(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		rel := fmt.Sprintf("images/collage-%d.jpg", i)
		writeRealCoverJPEG(t, f.env.cfg.DataDir, rel)
		it := &store.Item{
			ID: fmt.Sprintf("c%d", i), LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeMovie, Name: fmt.Sprintf("电影 %d", i),
			SortName: fmt.Sprintf("电影 %d", i), ImagePath: rel,
		}
		if err := f.env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	rr := f.do(http.MethodGet, "/Users/"+f.admin.ID+"/Views", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("views = %d", rr.Code)
	}
	var views dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	tag := views.Items[0].ImageTags["Primary"]
	if tag == "" {
		t.Fatal("missing collage tag")
	}
	cachePath := filepath.Join(f.env.cfg.DataDir, "library-images", lib.ID+"-collage-"+tag+".jpg")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("collage cache missing: %v", err)
	}
	img, err := decodeImageFileForTest(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != coverW || img.Bounds().Dy() != coverH {
		t.Fatalf("collage size = %v", img.Bounds())
	}

	// 换一条最新条目，签名必须变，触发重建。
	sources := []coverSource{
		{item: &store.Item{ID: "a", ImagePath: "images/a.jpg"}, full: "images/a.jpg"},
		{item: &store.Item{ID: "b", ImagePath: "images/b.jpg"}, full: "images/b.jpg"},
	}
	if collageSignature(lib, coverTitle{}, sources) == collageSignature(lib, coverTitle{}, sources[1:]) {
		t.Fatal("collage signature should change with item set")
	}
}

func decodeImageFileForTest(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
