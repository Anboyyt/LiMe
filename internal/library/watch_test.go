package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lime/internal/store"
)

func TestRealtimeWatcherIngestsNewSTRM(t *testing.T) {
	root := t.TempDir()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "auto"); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := New(db, logger)
	ingested := make(chan int, 1)

	w := NewWatcher(db, sc, logger, 100*time.Millisecond, func(ids []string) {
		select {
		case ingested <- len(ids):
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// 等初始 watch 建立；测试里目录很小。
	time.Sleep(300 * time.Millisecond)

	path := filepath.Join(root, "电影", "测试片 Test (2026) {tmdb-1}", "测试片.2026.1080p.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=999&size=1000&file_name=a.mp4")

	select {
	case n := <-ingested:
		if n < 1 {
			t.Fatalf("ingested %d, want >= 1", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not ingest new strm")
	}

	libs, err := db.ListLibraries()
	if err != nil {
		t.Fatal(err)
	}
	_, total, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("new strm was not written to db")
	}
}

func TestRealtimeWatcherDefersDuringScan(t *testing.T) {
	root := t.TempDir()

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

	// 模拟扫描进行中：watcher 应该把事件攒起来而不是丢掉。
	sc.mu.Lock()
	sc.running = true
	sc.mu.Unlock()

	w := NewWatcher(db, sc, logger, 100*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond)

	path := filepath.Join(root, "电影", "扫描中 Test (2026) {tmdb-3}", "扫描中.2026.1080p.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=777&size=1000&file_name=a.mp4")

	time.Sleep(time.Second)
	if n, _ := db.CountSources(lib.ID); n != 0 {
		t.Fatalf("ingested during scan: %d sources", n)
	}

	sc.mu.Lock()
	sc.running = false
	sc.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := db.CountSources(lib.ID); n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("deferred event was not processed after scan")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRealtimeWatcherRemovesDeletedSTRM(t *testing.T) {
	root := t.TempDir()

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
	w := NewWatcher(db, sc, logger, 100*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond)

	path := filepath.Join(root, "电影", "测试片 Test (2026) {tmdb-1}", "测试片.2026.1080p.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=999&size=1000&file_name=a.mp4")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := db.CountSources(lib.ID); n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new strm was not ingested")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		n, _ := db.CountSources(lib.ID)
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("deleted strm still has %d sources", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRealtimeReconcileAppliesOfflineChanges(t *testing.T) {
	root := t.TempDir()

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
	w := NewWatcher(db, sc, logger, 100*time.Millisecond, nil)

	gone := filepath.Join(root, "电影", "旧片 Old (2020) {tmdb-2}", "old.2020.1080p.strm")
	writeStrm(t, gone,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=101&size=1000&file_name=old.mp4")
	if _, _, err := sc.RefreshFilesWithIDs([]string{gone}); err != nil {
		t.Fatal(err)
	}

	// 模拟容器停机期间的变更：删一个、加一个；watcher 不在跑，收不到事件。
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, "电影", "新片 New (2026) {tmdb-3}", "new.2026.1080p.strm")
	writeStrm(t, fresh,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=202&size=1000&file_name=new.mp4")

	w.reconcile(context.Background())

	paths, err := db.SourcePathsUnder(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != fresh {
		t.Fatalf("sources after reconcile = %v, want only %s", paths, fresh)
	}
	if n, _ := db.CountSources(lib.ID); n != 1 {
		t.Fatalf("source count = %d, want 1", n)
	}
}

func TestRealtimeReconcileSkipsUnavailableRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

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
	w := NewWatcher(db, sc, logger, 100*time.Millisecond, nil)

	path := filepath.Join(root, "电影", "测试片 Test (2026) {tmdb-1}", "测试片.2026.1080p.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=999&size=1000&file_name=a.mp4")
	if _, _, err := sc.RefreshFilesWithIDs([]string{path}); err != nil {
		t.Fatal(err)
	}

	// 媒体盘没挂上：库根整个不存在，对账必须跳过，不能把整库片源清空。
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	w.reconcile(context.Background())

	if n, _ := db.CountSources(lib.ID); n != 1 {
		t.Fatalf("unavailable root cleared sources: %d, want 1", n)
	}
}

func TestRealtimeWatcherPicksUpLibraryRootCreatedLater(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "lib")

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
	w := NewWatcher(db, sc, logger, 100*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond) // 建立库路径父目录的 watch

	path := filepath.Join(root, "电影", "后建片 Later (2026) {tmdb-4}", "later.2026.1080p.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=303&size=1000&file_name=later.mp4")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := db.CountSources(lib.ID); n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not pick up library root created after startup")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
