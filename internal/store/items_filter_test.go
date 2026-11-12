package store

import (
	"testing"
	"time"
)

func TestQueryItemsPlaybackFilters(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','movies',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser("u1", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := db.UpsertItem(&Item{
			ID: id, LibraryID: "lib", ParentID: "lib",
			Type: TypeMovie, Name: id, SortName: id, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetPlayed(u.ID, "m1", true); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaystate(u.ID, "m2", 100, false); err != nil {
		t.Fatal(err)
	}

	names := func(q ItemQuery) []string {
		q.LibraryID = "lib"
		q.Recursive = true
		q.UserID = u.ID
		items, total, err := db.QueryItems(q)
		if err != nil {
			t.Fatal(err)
		}
		if total != len(items) {
			t.Fatalf("total=%d items=%d", total, len(items))
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Name)
		}
		return out
	}

	if got := names(ItemQuery{PlayedOnly: true}); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("played = %v, want [m1]", got)
	}
	if got := names(ItemQuery{UnplayedOnly: true}); len(got) != 2 {
		t.Fatalf("unplayed = %v, want 2 items", got)
	}
	if got := names(ItemQuery{ResumableOnly: true}); len(got) != 1 || got[0] != "m2" {
		t.Fatalf("resumable = %v, want [m2]", got)
	}
}
