package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

// logo 必须原样返回：带 maxWidth 也不能走 JPEG 缩放，否则透明通道会丢。
func TestItemLogoImage(t *testing.T) {
	env := newTestEnv(t)

	lib, err := env.db.UpsertLibrary("测试", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	logoRel := "images/logo.png"
	logoFull := filepath.Join(env.cfg.DataDir, filepath.FromSlash(logoRel))
	if err := os.MkdirAll(filepath.Dir(logoFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logoFull, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, Type: store.TypeMovie, Name: "电影",
		LogoPath: logoRel,
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet,
		"/Items/m1/Images/Logo?maxWidth=100", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "PNGDATA" {
		t.Fatalf("logo image: status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("logo content-type = %q, want image/png", ct)
	}
}

// 本地海报（local: 前缀）应直接从媒体目录读取，不需要复制到 data/images。
func TestItemLocalImage(t *testing.T) {
	root := t.TempDir()
	env := newTestEnvWithConfig(t, func(c *config.Config) {
		c.LibraryRoots = []string{root}
	})

	lib, err := env.db.UpsertLibrary("本地", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	poster := filepath.Join(root, "作品", "poster.jpg")
	if err := os.MkdirAll(filepath.Dir(poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, []byte("LOCALPOSTER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m-local", LibraryID: lib.ID, Type: store.TypeMovie, Name: "本地电影",
		ImagePath: "local:" + filepath.ToSlash(poster),
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/Items/m-local/Images/Primary", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "LOCALPOSTER" {
		t.Fatalf("local image: status=%d body=%q", rr.Code, rr.Body.String())
	}
}

// local: 路径必须限制在媒体库根目录内，越界一律 404。
func TestItemLocalImageTraversal(t *testing.T) {
	root := t.TempDir()
	env := newTestEnvWithConfig(t, func(c *config.Config) {
		c.LibraryRoots = []string{root}
	})
	lib, err := env.db.UpsertLibrary("本地", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "secret.jpg")
	_ = os.WriteFile(outside, []byte("SECRET"), 0o644)

	if err := env.db.UpsertItem(&store.Item{
		ID: "m-evil", LibraryID: lib.ID, Type: store.TypeMovie, Name: "越界",
		ImagePath: "local:" + filepath.ToSlash(outside),
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/Items/m-evil/Images/Primary", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestItemImage(t *testing.T) {
	env := newTestEnv(t)

	lib, err := env.db.UpsertLibrary("测试", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	// 准备一张本地海报
	imgRel := "images/poster.jpg"
	imgFull := filepath.Join(env.cfg.DataDir, filepath.FromSlash(imgRel))
	if err := os.MkdirAll(filepath.Dir(imgFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgFull, []byte("POSTER"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, Type: store.TypeMovie, Name: "电影",
		ImagePath: imgRel,
	}); err != nil {
		t.Fatal(err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		return rr
	}

	if rr := get("/Items/m1/Images/Primary"); rr.Code != http.StatusOK || rr.Body.String() != "POSTER" {
		t.Fatalf("primary image: status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr := get("/Items/m1/Images/Primary/0"); rr.Code != http.StatusOK {
		t.Fatalf("primary image with index: status=%d", rr.Code)
	}
	if rr := get("/Items/m1/Images/Backdrop"); rr.Code != http.StatusNotFound {
		t.Fatalf("missing backdrop: status=%d", rr.Code)
	}
	if rr := get("/Items/nope/Images/Primary"); rr.Code != http.StatusNotFound {
		t.Fatalf("missing item: status=%d", rr.Code)
	}

	// 目录穿越必须被挡住
	if _, err := env.db.Exec(`UPDATE items SET image_path = '../secret.jpg' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if rr := get("/Items/m1/Images/Primary"); rr.Code != http.StatusNotFound {
		t.Fatalf("traversal should 404, got %d", rr.Code)
	}
}
