package scrape

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"lime/internal/config"
	"lime/internal/store"
)

func TestScrapeStatusPersistsAcrossRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	s := New(cfg, db, log)
	s.mu.Lock()
	s.processed = 123
	s.total = 456
	s.lastFinished = 789
	s.lastError = "boom"
	s.mu.Unlock()
	s.saveStatus()

	s2 := New(cfg, db, log)
	st := s2.Status()
	if st.Processed != 123 || st.Total != 456 || st.LastFinished != 789 || st.LastError != "boom" {
		t.Fatalf("restored status = %+v", st)
	}
	if st.Running {
		t.Fatal("status should not be running after restart")
	}
}

func TestPartialTimeoutIsNotAnError(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(cfg, db, log)

	// 时长预算到期：返回 nil 错误，状态标记 Partial，面板不标红。
	n, err := s.runGuarded("test", func() (int, error) {
		return 7, context.DeadlineExceeded
	})
	if err != nil {
		t.Fatalf("deadline should not be an error, got %v", err)
	}
	if n != 7 {
		t.Fatalf("processed = %d, want 7", n)
	}
	st := s.Status()
	if !st.Partial || st.LastError != "" || st.LastNote == "" {
		t.Fatalf("status = %+v, want partial with note and no error", st)
	}

	// 重启后仍然显示「上次没跑完」，而不是错误。
	st = New(cfg, db, log).Status()
	if !st.Partial || st.LastError != "" || st.LastNote == "" {
		t.Fatalf("restored status = %+v", st)
	}
}

func TestFailureStreakOnlySetsGlobalErrorWhenPersistent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(cfg, db, log)

	// 单个条目失败不能把面板标红。
	s.noteFailure(errors.New("context deadline exceeded"))
	if got := s.Status().LastError; got != "" {
		t.Fatalf("single failure set global error: %q", got)
	}
	for i := 0; i < 19; i++ {
		s.noteFailure(errors.New("context deadline exceeded"))
	}
	if got := s.Status().LastError; got == "" {
		t.Fatal("persistent failures should set global error")
	}

	s.noteSuccess()
	if s.failStreak != 0 {
		t.Fatalf("failStreak = %d after success, want 0", s.failStreak)
	}
}

func TestScrapeTimeoutPersistsAcrossRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	s := New(cfg, db, log)
	if err := s.SetTimeoutSeconds(2 * 3600); err != nil {
		t.Fatal(err)
	}
	if got := s.Timeout(); got != 2*time.Hour {
		t.Fatalf("timeout = %v, want 2h", got)
	}

	// 面板保存的值优先于默认 6h，重启后仍然生效。
	if got := New(cfg, db, log).Timeout(); got != 2*time.Hour {
		t.Fatalf("restored timeout = %v, want 2h", got)
	}

	// 0 表示不限。
	if err := s.SetTimeoutSeconds(0); err != nil {
		t.Fatal(err)
	}
	if got := New(cfg, db, log).Timeout(); got != 0 {
		t.Fatalf("restored unlimited = %v, want 0", got)
	}
}
