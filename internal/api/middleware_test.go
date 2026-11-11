package api

import (
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
