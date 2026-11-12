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

// 容器停机期间删掉的文件不会产生 fsnotify 事件；每日增量扫描要兜底清理。
func TestFastScanRemovesFileDeletedWhileStopped(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "电影", "保留 (2020) {tmdb-1}", "keep.strm")
	gone := filepath.Join(root, "电影", "删除 (2021) {tmdb-2}", "gone.strm")
	writeStrm(t, keep, "http://host/strm/123/drive?drive_id=6&file_id=1&size=1000&file_name=a.mp4")
	writeStrm(t, gone, "http://host/strm/123/drive?drive_id=6&file_id=2&size=1000&file_name=b.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	lib, err := db.UpsertLibrary("test", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	sc := New(db, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.ScanAllFast(); err != nil {
		t.Fatal(err)
	}

	items, total, err := db.QueryItems(store.ItemQuery{LibraryID: lib.ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		for _, it := range items {
			t.Logf("item %s %s", it.Type, it.Name)
		}
		t.Fatalf("items = %d, want 1", total)
	}
	if items[0].Name != "保留" {
		t.Fatalf("kept item = %q, want 保留", items[0].Name)
	}
}

// 全量扫描时文件临时读不到（这里用空文件模拟解析失败），
// 条目不能被当成已删除清掉，播放进度也要保住。
func TestFullScanKeepsItemWhenParseFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "电影", "临时故障 (2020) {tmdb-1}", "movie.strm")
	writeStrm(t, path, "http://host/strm/123/drive?drive_id=6&file_id=1&size=1000&file_name=a.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	lib, err := db.UpsertLibrary("test", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	sc := New(db, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}
	items, _, _ := db.QueryItems(store.ItemQuery{LibraryID: lib.ID, Recursive: true})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	itemID := items[0].ID

	// 空文件 -> ParseFile 返回 ErrEmpty，条目不会被 upsert。
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.GetItem(itemID); err != nil {
		t.Fatalf("item should be kept, got: %v", err)
	}
	srcs, _ := db.MediaSourcesByItem(itemID)
	if len(srcs) != 1 {
		t.Fatalf("sources = %d, want 1", len(srcs))
	}
}
