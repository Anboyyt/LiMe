package library

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"lime/internal/store"
)

func TestScanStatusPersistsAcrossRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(db, log)
	s.mu.Lock()
	s.lastStarted = 111
	s.lastFinished = 222
	s.lastCount = 333
	s.lastError = "boom"
	s.mu.Unlock()
	s.saveScanStatus()

	s2 := New(db, log)
	st := s2.Status()
	if st.LastStarted != 111 || st.LastFinished != 222 || st.LastCount != 333 || st.LastError != "boom" {
		t.Fatalf("restored status = %+v", st)
	}
	if st.Running {
		t.Fatal("status should not be running after restart")
	}
}
