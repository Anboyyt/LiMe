package library

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"lime/internal/store"
)

// 增量扫描必须跳过 mtime/size 都没变的文件，否则每天都会重解析整个库。
func TestFastScanSkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	writeStrm(t, filepath.Join(root, "电影", "片名 (2020) {tmdb-1}", "a.strm"),
		"http://host/strm/123/drive?drive_id=6&file_id=1&size=1000&file_name=a.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "movies"); err != nil {
		t.Fatal(err)
	}
	sc := New(db, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))

	if n, err := sc.ScanAll(); err != nil || n != 1 {
		t.Fatalf("full scan = %d, %v", n, err)
	}
	if n, err := sc.ScanAllFast(); err != nil || n != 0 {
		t.Fatalf("fast scan = %d, %v; want 0 changed", n, err)
	}
}
