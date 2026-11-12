package library

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lime/internal/store"
)

// 目录改名（例如补上 {tmdb-ID}）会改变条目 ID，旧片源的
// (drive_id, cloud_file_id) 唯一索引仍指向旧行。扫描必须能接管这条片源，
// 而不是报 UNIQUE constraint failed 后留下一集「有卡片没片源」。
func TestScanRenameKeepsMediaSources(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "剧集", "Show Name (2020)", "Season 1")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStrm(t, filepath.Join(oldDir, "Show.Name.2020.S01E01.1080p.strm"),
		"http://192.168.1.155:16868/strm/115/drive?drive_id=5&file_id=777&size=1000&file_name=a.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("剧集", root, "tvshows"); err != nil {
		t.Fatal(err)
	}
	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(
		filepath.Join(root, "剧集", "Show Name (2020)"),
		filepath.Join(root, "剧集", "Show Name (2020) {tmdb-340134}"),
	); err != nil {
		t.Fatal(err)
	}
	// 扫描清理按秒比较 updated_at，两次扫描挤在同一秒会留下旧条目。
	time.Sleep(1100 * time.Millisecond)
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	items, _, err := db.QueryItems(store.ItemQuery{Recursive: true, Types: []string{store.TypeEpisode}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("episodes = %d, want 1", len(items))
	}
	srcs, err := db.MediaSourcesByItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 {
		t.Fatalf("sources = %d, want 1", len(srcs))
	}
}
