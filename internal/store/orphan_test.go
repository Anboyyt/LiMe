package store

import (
	"testing"
	"time"
)

func TestPurgeOrphanItems(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	now := time.Now()
	items := []*Item{
		{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "有片源", SortName: "a", CreatedAt: now},
		{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "没片源", SortName: "b", CreatedAt: now},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "s1", ItemID: "m1", StrmPath: "/strm/m1.strm", URL: "http://x/1",
	}); err != nil {
		t.Fatal(err)
	}

	n, err := db.PurgeOrphanItems()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	if _, err := db.GetItem("m1"); err != nil {
		t.Fatal("item with source should stay")
	}
	if _, err := db.GetItem("m2"); err == nil {
		t.Fatal("orphan item should be removed")
	}
}

func TestPurgeOrphanItemsSkipsEmptySourceTable(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "x", SortName: "x", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := db.PurgeOrphanItems()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("purged = %d, want 0 when source table empty", n)
	}
	if _, err := db.GetItem("m1"); err != nil {
		t.Fatal("item should be kept when source table is empty")
	}
}
