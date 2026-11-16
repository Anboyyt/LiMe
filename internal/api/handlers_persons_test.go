package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/config"
	"lime/internal/dto"
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

// 收藏页会请求 /Persons?IsFavorite=true。LiMe 没有人物收藏数据，
// 必须返回空，不能把全部演员当成收藏。
func TestPersonsFavoriteReturnsEmpty(t *testing.T) {
	f := newUserFixture(t)
	if err := f.env.db.UpsertPersons([]store.PersonImage{
		{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Actor A"},
		{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "Actor B"},
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Persons?IsFavorite=true", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("favorite status = %d", rr.Code)
	}
	var res dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.TotalRecordCount != 0 || len(res.Items) != 0 {
		t.Fatalf("favorite persons = %+v", res)
	}

	// 不带收藏条件时仍然返回全部演员（演员浏览页）。
	rr = f.do(http.MethodGet, "/Persons?Limit=10", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.TotalRecordCount != 2 {
		t.Fatalf("all persons total = %d", res.TotalRecordCount)
	}
}
