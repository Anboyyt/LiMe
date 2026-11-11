package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"lime/internal/store"
)

func TestRemoveMissingCleansItems(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "电影", "测试片 Test (2026) {tmdb-1}")
	movie := filepath.Join(movieDir, "测试片.2026.1080p.strm")
	seriesDir := filepath.Join(root, "剧集", "测试剧 (2026) {tmdb-2}")
	episode := filepath.Join(seriesDir, "Season 1", "测试剧.2026.S01E01.strm")

	writeStrm(t, movie, "http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=1&size=1000&file_name=a.mp4")
	writeStrm(t, episode, "http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=2&size=1000&file_name=b.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	lib, err := db.UpsertLibrary("test", root, "auto")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := New(db, logger)
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	count := func() int {
		_, total, err := db.QueryItems(store.ItemQuery{LibraryID: lib.ID, Recursive: true})
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	if got := count(); got != 4 { // 电影 + 剧 + 季 + 集
		items, _, _ := db.QueryItems(store.ItemQuery{LibraryID: lib.ID, Recursive: true})
		for _, it := range items {
			t.Logf("item type=%s name=%s parent=%s", it.Type, it.Name, it.ParentID)
		}
		t.Fatalf("items after scan = %d, want 4", got)
	}

	// 文件还在时不应该误删。
	if n, err := sc.RemoveMissing([]string{movie}); err != nil || n != 0 {
		t.Fatalf("RemoveMissing existing = %d, %v", n, err)
	}

	if err := os.Remove(movie); err != nil {
		t.Fatal(err)
	}
	if n, err := sc.RemoveMissing([]string{movie}); err != nil || n != 1 {
		t.Fatalf("RemoveMissing movie = %d, %v", n, err)
	}
	if got := count(); got != 3 {
		t.Fatalf("items after movie removal = %d, want 3", got)
	}

	// 整个剧集目录被删：应该连季和剧一起清掉。
	if err := os.RemoveAll(seriesDir); err != nil {
		t.Fatal(err)
	}
	if n, err := sc.RemoveMissing([]string{seriesDir}); err != nil || n != 1 {
		t.Fatalf("RemoveMissing series dir = %d, %v", n, err)
	}
	if got := count(); got != 0 {
		t.Fatalf("items after series removal = %d, want 0", got)
	}
}
