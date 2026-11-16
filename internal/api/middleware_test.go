package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanonicalAPIPath(t *testing.T) {
	cases := map[string]string{
		"/videos/x/stream.mkv":    "/Videos/x/stream.mkv",
		"/Videos/x/stream":        "/Videos/x/stream",
		"/emby/videos/x/stream":   "/emby/videos/x/stream", // 前缀由 stripEmbyPrefix 处理
		"/items/x/Similar":        "/Items/x/Similar",
		"/Users/x/Images/Primary": "/Users/x/Images/Primary",
		"/web/":                   "/web/",
		"/app/app.js":             "/app/app.js",
		"/health":                 "/health",
		"/":                       "/",
	}
	for in, want := range cases {
		if got := canonicalAPIPath(in); got != want {
			t.Errorf("canonicalAPIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLowercaseEmbyVideoRoute(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/emby/videos/missing/stream.mkv?static=true", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// 路由命中了 handler；不是 Go mux 的默认 404 文本。
	if strings.Contains(rr.Body.String(), "404 page not found") {
		t.Fatalf("lowercase route did not match: %s", rr.Body.String())
	}
}

// panic 不能只断连接：能补 500 就补，并记录堆栈。
func TestWithRecoveryReturns500(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := withRecovery(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

// 已经写过响应头时不能再补 500（否则会重复写 header）。
func TestWithRecoveryAfterHeadersWritten(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := withRecovery(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("boom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want original 200", rr.Code)
	}
}
