package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lime/internal/config"
	"lime/internal/store"
)

func testResolveHandler(auto bool) *Handler {
	h := &Handler{
		cfg: config.Config{ResolveRedirect: true, ResolveRedirectAuto: auto},
		log: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	h.resolveRedirect.Store(true)
	h.resolveAuto.Store(auto)
	return h
}

func TestResolveURLAutoFallsBackAndBreaksCircuit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	h := testResolveHandler(true)
	for i := 0; i < autoResolveMaxFailures; i++ {
		if _, err := h.resolveURL(context.Background(), srv.URL, "SenPlayer/1.0", 200*time.Millisecond); err == nil {
			t.Fatal("expected upstream error")
		}
	}
	if !h.resolveBlocked() {
		t.Fatal("circuit should be open after repeated failures")
	}
	if _, err := h.resolveURL(context.Background(), srv.URL, "SenPlayer/1.0", 200*time.Millisecond); !errors.Is(err, errAutoResolveBlocked) {
		t.Fatalf("blocked err = %v", err)
	}
}

func TestResolveURLStrictReturnsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	h := testResolveHandler(false)
	_, err := h.resolveURL(context.Background(), srv.URL, "SenPlayer/1.0", 200*time.Millisecond)
	var se upstreamStatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want upstreamStatusError", err)
	}
	if h.resolveBlocked() {
		t.Fatal("strict mode should not trip the auto circuit")
	}
}

func TestDirectURLClientAllowed(t *testing.T) {
	h := &Handler{cfg: config.Config{PlaybackDirectURLAuto: true}}
	h.playbackDirectAt.Store(true)
	if !h.directURLClientAllowed("SenPlayer/1.0") {
		t.Fatal("SenPlayer should be allowed")
	}
	if !h.directURLClientAllowed("VidHub/3.0") {
		t.Fatal("VidHub should be allowed")
	}
	if h.directURLClientAllowed("Mozilla/5.0 Chrome/120") {
		t.Fatal("browser should not pre-resolve DirectStreamUrl")
	}
	h.playbackDirectAt.Store(false)
	if !h.directURLClientAllowed("Mozilla/5.0 Chrome/120") {
		t.Fatal("strict explicit mode should allow all clients")
	}
}

// auto 模式下游 4xx 必须回退原 302，而不是 502。
func TestStreamAutoFallsBackOnUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()

	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.ResolveRedirect = true
		c.ResolveRedirectAuto = true
	})
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", URL: upstream.URL, Container: "mkv",
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.doUA(http.MethodGet, "/Videos/m1/stream?static=true", nil, "SenPlayer/1.0", f.adminTok)
	if rr.Code != http.StatusFound {
		t.Fatalf("auto status = %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != upstream.URL {
		t.Fatalf("auto Location = %q, want %q", got, upstream.URL)
	}
}

// 显式 true 保持原有严格行为：上游 4xx 直接 502。
// strict 解析失败也不再 502：回退原始 302，让客户端自己跟一跳。
func TestStreamStrictFallsBackToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()

	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.ResolveRedirect = true
		c.ResolveRedirectAuto = false
	})
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", URL: upstream.URL, Container: "mkv",
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.doUA(http.MethodGet, "/Videos/m1/stream?static=true", nil, "SenPlayer/1.0", f.adminTok)
	if rr.Code != http.StatusFound {
		t.Fatalf("strict status = %d, want 302: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != upstream.URL {
		t.Fatalf("location = %q, want %q", got, upstream.URL)
	}
}
