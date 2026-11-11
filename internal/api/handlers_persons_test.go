package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

// 头像第一次从 TMDB 下载并落盘，第二次直接读缓存（源站已关闭也能返回）。
func TestPersonImageDownloadAndServe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))

	h := &Handler{
		cfg: config.Config{DataDir: t.TempDir()},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p := &store.PersonImage{ID: "abcdef0123456789abcdef0123456789", Name: "Test", ImageURL: srv.URL}

	rr := httptest.NewRecorder()
	h.servePersonImageFile(rr, httptest.NewRequest(http.MethodGet, "/", nil), p)
	if rr.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %q", ct)
	}

	srv.Close()

	rr = httptest.NewRecorder()
	h.servePersonImageFile(rr, httptest.NewRequest(http.MethodGet, "/", nil), p)
	if rr.Code != http.StatusOK || rr.Body.Len() != 4 {
		t.Fatalf("cached status = %d len = %d", rr.Code, rr.Body.Len())
	}
}
