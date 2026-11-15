package store

import (
	"testing"
	"time"
)

func TestItemGenresIndexAndFilter(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	if err := db.EnsureItemGenresIndex(); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "m1", SortName: "m1", Genres: `["动作","动画"]`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, total, err := db.QueryItems(ItemQuery{Genres: []string{"动作"}}); err != nil || total != 1 {
		t.Fatalf("genre filter total=%d err=%v", total, err)
	}
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "m1", SortName: "m1", Genres: `["喜剧"]`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := db.QueryItems(ItemQuery{Genres: []string{"动作"}}); total != 0 {
		t.Fatalf("old genre still matches: %d", total)
	}
	if _, total, _ := db.QueryItems(ItemQuery{Genres: []string{"喜剧"}}); total != 1 {
		t.Fatalf("new genre not indexed: %d", total)
	}
}

func TestBackfillItemGenres(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "m1", SortName: "m1", Genres: `["动作"]`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM item_genres`); err != nil {
		t.Fatal(err)
	}
	n, err := db.BackfillItemGenres()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backfilled = %d, want 1", n)
	}
}
