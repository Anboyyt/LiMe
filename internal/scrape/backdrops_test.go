package scrape

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lime/internal/config"
	"lime/internal/store"
)

func TestRefreshBackdropPicksLargestAndReplaces(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO libraries (id,name,path,kind,created_at) VALUES ('lib','测试','/strm','movies',?)`,
		time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib",
		Type: store.TypeMovie, Name: "电影", TmdbID: "123",
		BackdropPath: "images/old.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(dir, "images", "old.jpg")
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/movie/123/images":
			_, _ = w.Write([]byte(`{"backdrops":[
				{"file_path":"/small.jpg","width":1280,"height":720,"vote_average":9.0},
				{"file_path":"/big.jpg","width":3840,"height":2160,"vote_average":5.0}
			]}`))
		case r.URL.Path == "/original/small.jpg":
			_, _ = w.Write([]byte("SMALL"))
		case r.URL.Path == "/original/big.jpg":
			_, _ = w.Write([]byte("BIG"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New(config.Config{
		DataDir:       dir,
		TMDBAPIKeys:   []string{"test-key"},
		TMDBLanguage:  "zh-CN",
		TMDBBaseURL:   srv.URL,
		TMDBImageBase: srv.URL,
	}, db, slog.Default())
	s.paceInterval = 0

	if err := s.refreshBackdrop(context.Background(), store.BackdropItem{
		ID: "m1", Type: store.TypeMovie, TmdbID: "123", BackdropPath: "images/old.jpg",
	}); err != nil {
		t.Fatalf("refreshBackdrop: %v", err)
	}

	it, err := db.GetItem("m1")
	if err != nil {
		t.Fatal(err)
	}
	if it.BackdropPath == "" || it.BackdropPath == "images/old.jpg" {
		t.Fatalf("backdrop 未更新：%q", it.BackdropPath)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(it.BackdropPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BIG" {
		t.Fatalf("下载的不是最大分辨率图：%q", data)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("旧背景图应被删除，stat err = %v", err)
	}
}
