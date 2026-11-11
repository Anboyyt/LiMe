package store

import (
	"testing"
	"time"
)

func TestPlaybackHistoryRetention(t *testing.T) {
	db := newTestDB(t)

	// 8 天前的记录在表里，但下一条写入时应该被清掉。
	if err := db.AddPlaybackHistory(&PlaybackHistory{
		UserID: "u1", UserName: "u", ItemID: "i1", ItemName: "old",
		Event: "start", CreatedAt: time.Now().Add(-8 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.AddPlaybackHistory(&PlaybackHistory{
		UserID: "u1", UserName: "u", ItemID: "i2", ItemName: "new",
		Event: "stop", PositionTicks: 100,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := db.PlaybackHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d (%+v), want 1", len(rows), rows)
	}
	if rows[0].Event != "stop" || rows[0].ItemName != "new" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestPlaybackSessionPaused(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if _, found, err := db.PlaybackSessionPaused("missing"); err != nil || found {
		t.Fatalf("missing session: found=%v err=%v", found, err)
	}

	if err := db.UpsertPlaybackSession(&PlaybackSession{
		ID: "ps1", UserID: "u1", DeviceID: "d1", ItemID: "m1", IsPaused: true,
	}); err != nil {
		t.Fatal(err)
	}
	paused, found, err := db.PlaybackSessionPaused("ps1")
	if err != nil || !found || !paused {
		t.Fatalf("paused=%v found=%v err=%v", paused, found, err)
	}
}
