package store

import (
	"reflect"
	"testing"
	"time"
)

func TestQueryItemsSkipTotal(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','movies',?)`,
		time.Now().Unix(),
	); err != nil {
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

	items, total, err := db.QueryItems(ItemQuery{Types: []string{TypeMovie}, Limit: 2, SkipTotal: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("SkipTotal total = %d, want 0", total)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}

	_, total, err = db.QueryItems(ItemQuery{Types: []string{TypeMovie}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("normal total = %d, want 3", total)
	}
}

func TestGenreNamesInDedupAndSort(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','movies',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ id, genres string }{
		{"m1", `["动作","动画"]`},
		{"m2", `["动画","喜剧"]`},
		{"m3", ``},
	}
	for _, r := range rows {
		if err := db.UpsertItem(&Item{
			ID: r.id, LibraryID: "lib", ParentID: "lib",
			Type: TypeMovie, Name: r.id, SortName: r.id, Genres: r.genres, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GenreNamesIn(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"动作", "动画", "喜剧"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("genres = %v, want %v", got, want)
	}
}
