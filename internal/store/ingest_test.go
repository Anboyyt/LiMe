package store

import (
	"testing"
	"time"
)

func TestIngestNotificationQueue(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "电影", SortName: "电影", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	src := &MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/电影/m1.strm",
		URL: "http://example/1", Cloud: "115", Size: 123,
	}
	if err := db.UpsertMediaSource(src); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	n, err := db.CountPendingIngest(now + 5)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}

	rows, err := db.PendingIngestSources(now+5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "src1" || rows[0].ItemID != "m1" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].IngestedAt <= 0 {
		t.Fatalf("ingested_at = %d, want > 0", rows[0].IngestedAt)
	}

	// 重扫同一片源：不能重新入队。
	if err := db.UpsertMediaSource(src); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountPendingIngest(now + 5); n != 1 {
		t.Fatalf("after upsert pending = %d, want 1", n)
	}

	if err := db.MarkIngestNotified([]string{"src1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountPendingIngest(now + 5); n != 0 {
		t.Fatalf("after mark pending = %d, want 0", n)
	}

	// 已通知片源重扫后仍保持已通知。
	if err := db.UpsertMediaSource(src); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountPendingIngest(now + 5); n != 0 {
		t.Fatalf("after re-upsert pending = %d, want 0", n)
	}
}

func TestMarkAllPendingIngestNotified(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	for _, id := range []string{"m1", "m2"} {
		if err := db.UpsertItem(&Item{
			ID: id, LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
			Name: id, SortName: id, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertMediaSource(&MediaSource{
			ID: "src-" + id, ItemID: id, StrmPath: "/strm/" + id + ".strm",
			URL: "http://example/" + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := db.CountPendingIngest(time.Now().Unix() + 5); n != 2 {
		t.Fatalf("pending = %d, want 2", n)
	}
	affected, err := db.MarkAllPendingIngestNotified(time.Now().Unix()+5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	if n, _ := db.CountPendingIngest(time.Now().Unix() + 5); n != 0 {
		t.Fatalf("pending after mark all = %d, want 0", n)
	}
}
