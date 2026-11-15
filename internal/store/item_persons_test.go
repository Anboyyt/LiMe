package store

import (
	"testing"
	"time"
)

func seedLibrary(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','movies',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
}

func TestItemPersonsIndexAndFilter(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	if err := db.EnsureItemPersonsIndex(); err != nil {
		t.Fatal(err)
	}
	for id, people := range map[string]string{
		"m1": `[{"Name":"Tom Hanks"}]`,
		"m2": `[{"Name":"其他演员"}]`,
	} {
		if err := db.UpsertItem(&Item{
			ID: id, LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
			Name: id, SortName: id, People: people, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := db.QueryItems(ItemQuery{Persons: []string{"tom hanks"}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("person filter = %d/%v", total, items)
	}

	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "m1", SortName: "m1", People: `[{"Name":"新演员"}]`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = db.QueryItems(ItemQuery{Persons: []string{"Tom Hanks"}}); total != 0 {
		t.Fatalf("old person still matches: %d", total)
	}
	if _, total, _ = db.QueryItems(ItemQuery{Persons: []string{"新演员"}}); total != 1 {
		t.Fatalf("new person not indexed: %d", total)
	}
}

func TestBackfillItemPersons(t *testing.T) {
	db := newTestDB(t)
	seedLibrary(t, db)
	// 触发器创建前写入，模拟老库升级。
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "m1", SortName: "m1", People: `[{"Name":"老演员"}]`, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// 模拟老库：触发器不存在时写入的行没有进 item_persons。
	if _, err := db.Exec(`DELETE FROM item_persons`); err != nil {
		t.Fatal(err)
	}
	n, err := db.BackfillItemPersons()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backfilled = %d, want 1", n)
	}
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_persons WHERE item_id='m1'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("item_persons rows = %d", got)
	}
}
