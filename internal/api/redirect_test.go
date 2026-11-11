package api

import (
	"context"
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
