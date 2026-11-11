package scrape

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

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
