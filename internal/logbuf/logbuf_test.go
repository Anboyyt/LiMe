package logbuf

import (
	"io"
	"log/slog"
	"testing"
)

func TestHandlerCapturesAndFilters(t *testing.T) {
	buf := &Buffer{max: 3}
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), buf))

	log.Info("a", "k", 1)
	log.Warn("b")
	log.Error("c")

	all := buf.Snapshot(10, "")
	if len(all) != 3 || all[0].Message != "c" || all[2].Message != "a" {
		t.Fatalf("snapshot = %+v", all)
	}
	if all[2].Attrs["k"] != "1" {
		t.Fatalf("attrs = %+v", all[2].Attrs)
	}

	warn := buf.Snapshot(10, "WARN")
	if len(warn) != 2 || warn[0].Message != "c" || warn[1].Message != "b" {
		t.Fatalf("warn snapshot = %+v", warn)
	}

	// 超过容量后丢最旧的。
	log.Info("d")
	all = buf.Snapshot(10, "")
	if len(all) != 3 || all[0].Message != "d" || all[2].Message != "b" {
		t.Fatalf("ring snapshot = %+v", all)
	}
}
