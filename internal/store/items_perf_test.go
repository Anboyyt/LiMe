package store

import (
	"fmt"
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

func TestQueryItemsRandomUsesWindow(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := db.UpsertItem(&Item{
			ID: id, LibraryID: "lib", ParentID: "lib",
			Type: TypeMovie, Name: id, SortName: id, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := db.QueryItems(ItemQuery{
		Types: []string{TypeMovie}, SortBy: []string{"Random"}, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("random items = %d, want 2", len(items))
	}
}

// SkipTotal 不能把随机排序退化成「永远取 rowid 最前面那一批」。
// 库要大于随机窗口（Limit*50，最低 1000）才测得出偏移。
func TestQueryItemsRandomWithSkipTotalStillRandom(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO items
		(id, library_id, parent_id, type, name, sort_name, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := 0; i < 1200; i++ {
		id := fmt.Sprintf("m%04d", i)
		if _, err := stmt.Exec(id, "lib", "lib", TypeMovie, id, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for i := 0; i < 8; i++ {
		items, total, err := db.QueryItems(ItemQuery{
			Types: []string{TypeMovie}, SortBy: []string{"Random"},
			Limit: 2, SkipTotal: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if total != 0 {
			t.Fatalf("SkipTotal total = %d, want 0", total)
		}
		if len(items) != 2 {
			t.Fatalf("items = %d, want 2", len(items))
		}
		seen[items[0].ID+","+items[1].ID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("random results never changed across 8 calls: %v", seen)
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
