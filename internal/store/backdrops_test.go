package store

import "testing"

func TestBackdropBackfillItemsAndReplace(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 只给电影/剧集 TMDB ID；季/集不单独回填。
	if _, err := db.Exec(`UPDATE items SET tmdb_id = '100' WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET tmdb_id = '200' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	items, err := db.BackdropBackfillItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("BackdropBackfillItems = %d 条，期望 2 条", len(items))
	}

	// 剧集和它的季/集共享同一个背景图，替换时必须整组更新。
	for _, id := range []string{"s1", "se1", "e1", "e2"} {
		if _, err := db.Exec(`UPDATE items SET backdrop_path = 'images/old.jpg' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceBackdrop("s1", "images/old.jpg", "images/new.jpg"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "se1", "e1", "e2"} {
		it, err := db.GetItem(id)
		if err != nil {
			t.Fatal(err)
		}
		if it.BackdropPath != "images/new.jpg" {
			t.Fatalf("%s backdrop = %q", id, it.BackdropPath)
		}
	}
	if n, err := db.CountBackdropRefs("images/old.jpg"); err != nil || n != 0 {
		t.Fatalf("旧图引用数 = %d, err = %v", n, err)
	}
}
