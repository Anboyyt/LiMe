package api

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"lime/internal/config"
	"lime/internal/store"
)

func newStallTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "lime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return &Handler{
		cfg: config.Config{
			StallAlertAfter:    time.Millisecond,
			StallAlertCooldown: time.Minute,
		},
		db:  db,
		log: slog.Default(),
	}
}

func TestTrackStallRecordsStuckProgress(t *testing.T) {
	h := newStallTestHandler(t)
	user := &store.User{ID: "u1", Name: "Lime"}
	it := &store.Item{ID: "i1", Name: "测试片", Type: store.TypeMovie}
	a := authInfo{Device: "电视", Client: "SenPlayer", DeviceID: "dev1"}

	h.trackStall(user, a, "sess1", it, "src1", 0, false)
	time.Sleep(10 * time.Millisecond)
	h.trackStall(user, a, "sess1", it, "src1", 0, false)
	time.Sleep(80 * time.Millisecond)

	rows, err := h.db.ListPlaybackStalls(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("stalls = %d, want 1", len(rows))
	}
	if rows[0].SessionID != "sess1" || rows[0].ItemID != "i1" || rows[0].UserName != "Lime" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
}

func TestTrackStallIgnoresPausedAndSeek(t *testing.T) {
	h := newStallTestHandler(t)
	user := &store.User{ID: "u1", Name: "Lime"}
	it := &store.Item{ID: "i1", Name: "测试片", Type: store.TypeMovie}
	a := authInfo{Device: "电视"}

	h.trackStall(user, a, "sess1", it, "", 10_000_000, false)
	time.Sleep(10 * time.Millisecond)
	h.trackStall(user, a, "sess1", it, "", 10_000_000, true) // 暂停
	time.Sleep(10 * time.Millisecond)
	h.trackStall(user, a, "sess1", it, "", 900_000_000, false) // 拖动
	time.Sleep(60 * time.Millisecond)

	rows, _ := h.db.ListPlaybackStalls(0, 10)
	if len(rows) != 0 {
		t.Fatalf("paused/seek should not alert, got %d", len(rows))
	}
}

// 暂停期间客户端可能完全不上报；恢复后的第一条心跳位置没变，
// 不能把整段静默算成卡顿。
func TestTrackStallIgnoresSilenceGap(t *testing.T) {
	h := newStallTestHandler(t)
	user := &store.User{ID: "u1", Name: "Lime"}
	it := &store.Item{ID: "i1", Name: "测试片", Type: store.TypeMovie}
	a := authInfo{Device: "手机"}

	h.trackStall(user, a, "sess1", it, "", 1_000_000, false)
	// 模拟暂停 10 分钟：期间没有任何进度上报。
	h.stallMu.Lock()
	h.stallWatch["sess1"].lastSeen = time.Now().Add(-10 * time.Minute)
	h.stallMu.Unlock()
	h.trackStall(user, a, "sess1", it, "", 1_000_000, false)

	rows, _ := h.db.ListPlaybackStalls(0, 10)
	if len(rows) != 0 {
		t.Fatalf("silence gap should not alert, got %d", len(rows))
	}
}

// 暂停时只上报一次 IsPaused=true，后续心跳不带暂停标记也不能误报。
func TestTrackStallPausedLatches(t *testing.T) {
	h := newStallTestHandler(t)
	user := &store.User{ID: "u1", Name: "Lime"}
	it := &store.Item{ID: "i1", Name: "测试片", Type: store.TypeMovie}
	a := authInfo{Device: "手机"}

	h.trackStall(user, a, "sess1", it, "", 1_000_000, true)
	time.Sleep(10 * time.Millisecond)
	h.trackStall(user, a, "sess1", it, "", 1_000_000, false) // 心跳省略 IsPaused
	time.Sleep(50 * time.Millisecond)
	h.trackStall(user, a, "sess1", it, "", 1_000_000, false)

	rows, _ := h.db.ListPlaybackStalls(0, 10)
	if len(rows) != 0 {
		t.Fatalf("paused heartbeat should not alert, got %d", len(rows))
	}
}
