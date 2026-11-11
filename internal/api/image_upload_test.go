package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lime/internal/store"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadImage(t *testing.T, env *testEnv, tok, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("X-Emby-Token", tok)
	req.Header.Set("Content-Type", "image/png")
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	return rr
}

// 封面生成器通过 POST /Items/{id}/Images/Primary 写回，必须返回 204 且能读回。
func TestUploadItemImage(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}

	png := pngBytes(t)
	rr := uploadImage(t, env, tok, "/emby/Items/m1/Images/Primary", png)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", rr.Code, rr.Body.String())
	}

	it, err := env.db.GetItem("m1")
	if err != nil {
		t.Fatal(err)
	}
	if it.ImagePath == "" {
		t.Fatal("image_path not updated")
	}

	req := httptest.NewRequest(http.MethodGet, "/emby/Items/m1/Images/Primary", nil)
	req.Header.Set("X-Emby-Token", tok)
	get := httptest.NewRecorder()
	env.handler.ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d", get.Code)
	}
	if !bytes.Equal(get.Body.Bytes(), png) {
		t.Fatal("served image differs from uploaded bytes")
	}
}

// 库封面：库 ID 不是条目，也要接受上传，并覆盖自动生成的库封面。
func TestUploadLibraryImage(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("国漫", "/strm/anime", "tvshows")
	if err != nil {
		t.Fatal(err)
	}

	png := pngBytes(t)
	rr := uploadImage(t, env, tok, "/emby/Items/"+lib.ID+"/Images/Primary", png)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/emby/Items/"+lib.ID+"/Images/Primary", nil)
	req.Header.Set("X-Emby-Token", tok)
	get := httptest.NewRecorder()
	env.handler.ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", get.Code, get.Body.String())
	}
	if !bytes.Equal(get.Body.Bytes(), png) {
		t.Fatal("served library image differs from uploaded bytes")
	}
}

func TestDeleteUploadedItemImage(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}

	if rr := uploadImage(t, env, tok, "/emby/Items/m1/Images/Primary", pngBytes(t)); rr.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodDelete, "/emby/Items/m1/Images/Primary", nil)
	req.Header.Set("X-Emby-Token", tok)
	del := httptest.NewRecorder()
	env.handler.ServeHTTP(del, req)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", del.Code, del.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/emby/Items/m1/Images/Primary", nil)
	get.Header.Set("X-Emby-Token", tok)
	grr := httptest.NewRecorder()
	env.handler.ServeHTTP(grr, get)
	if grr.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", grr.Code)
	}
}

// TgtoDrive 会把图片 base64 后 POST；必须解码成真实图片再落盘。
func TestUploadBase64Image(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}

	png := pngBytes(t)
	for _, body := range [][]byte{
		[]byte(base64.StdEncoding.EncodeToString(png)),
		[]byte("data:image/png;base64," + base64.StdEncoding.EncodeToString(png)),
	} {
		rr := uploadImage(t, env, tok, "/emby/Items/m1/Images/Primary", body)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("upload status = %d, body = %s", rr.Code, rr.Body.String())
		}
		it, err := env.db.GetItem("m1")
		if err != nil {
			t.Fatal(err)
		}
		stored, err := os.ReadFile(filepath.Join(env.cfg.DataDir, filepath.FromSlash(it.ImagePath)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, png) {
			t.Fatalf("stored bytes are not the decoded image (len %d)", len(stored))
		}
	}
}

// 部分客户端会在类型后面带 index 段。
func TestUploadItemImageWithIndexSegment(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}

	rr := uploadImage(t, env, tok, "/emby/Items/m1/Images/Primary/0", pngBytes(t))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestIsAnimatedPNG(t *testing.T) {
	plain := pngBytes(t)
	if isAnimatedPNG(plain) {
		t.Fatal("static png misdetected as apng")
	}
	apng := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("acTL")...)
	apng = append(apng, make([]byte, 20)...)
	if !isAnimatedPNG(apng) {
		t.Fatal("apng not detected")
	}
	if isAnimatedPNG([]byte("not an image")) {
		t.Fatal("garbage misdetected")
	}
}
