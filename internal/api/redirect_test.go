package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://cdn.example.com/video.mkv?x=1&y=2", http.StatusFound)
	}))
	defer srv.Close()

	got, err := resolveRedirect(context.Background(), srv.URL, "VidHub/3.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/video.mkv?x=1&y=2" {
		t.Fatalf("resolved = %q", got)
	}
}

// 上游 4xx/5xx 必须作为错误上报，否则调用方会把坏地址 302 给客户端。
func TestResolveRedirectUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := resolveRedirect(context.Background(), srv.URL, "")
	var se upstreamStatusError
	if !errors.As(err, &se) || se.status != http.StatusForbidden {
		t.Fatalf("expected upstreamStatusError 403, got %v", err)
	}
}

func TestResolveRedirectMissingLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound) // 3xx 但没有 Location
	}))
	defer srv.Close()

	if _, err := resolveRedirect(context.Background(), srv.URL, ""); err == nil {
		t.Fatal("expected error for redirect without location")
	}
}

func TestResolveRedirectNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got, err := resolveRedirect(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty for 200, got %q", got)
	}
}
