package store

import "testing"

func TestTMDBImagePath(t *testing.T) {
	cases := map[string]bool{
		"images/0123456789abcdef01234567.jpg": true,
		"images/0123456789abcdef01234567.png": true,
		"images/0123456789abcdef0123456.jpg":  false, // 23 位
		"images/0123456789ABCDEF01234567.jpg": false, // 大写
		"library-images/x-custom.jpg":         false,
		"images/abc-primary-1700000000.jpg":   false, // 面板上传
		"images/0123456789abcdef01234567.gif": false, // 动图
		"":                                    false,
	}
	for rel, want := range cases {
		if got := TMDBImagePath(rel); got != want {
			t.Fatalf("TMDBImagePath(%q) = %v, want %v", rel, got, want)
		}
	}
}

// 空海报的条目也要进回填队列，否则「刮过但没图」的条目永远没有重试机会。
func TestPosterBackfillItemsIncludesMissing(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 再补两条干扰项：本地海报、面板手工上传的封面。
	extra := []*Item{
		{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "本地海报", SortName: "本地海报"},
		{ID: "m3", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "手工封面", SortName: "手工封面"},
		{ID: "m4", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "无 TMDB", SortName: "无 TMDB"},
	}
	for _, it := range extra {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert %s: %v", it.ID, err)
		}
	}

	rel := "images/0123456789abcdef01234567.jpg"
	stmts := []struct {
		sql  string
		args []any
	}{
		{`UPDATE items SET tmdb_id='1', image_path=? WHERE id='s1'`, []any{rel}},
		{`UPDATE items SET tmdb_id='2', image_path='' WHERE id='m1'`, nil},
		{`UPDATE items SET tmdb_id='3', image_path='local:/strm/x/poster.jpg' WHERE id='m2'`, nil},
		{`UPDATE items SET tmdb_id='4', image_path='images/abc-primary-1700000000.jpg' WHERE id='m3'`, nil},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed poster: %v", err)
		}
	}

	items, err := db.PosterBackfillItems()
	if err != nil {
		t.Fatalf("PosterBackfillItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (空海报 + TMDB 海报): %+v", len(items), items)
	}
	// 空海报排最前，优先补。
	if items[0].ID != "m1" || items[0].ImagePath != "" {
		t.Fatalf("first item = %+v, want m1 with empty poster", items[0])
	}
	if items[1].ID != "s1" {
		t.Fatalf("second item = %+v, want s1", items[1])
	}
}

// SetPoster 只能改指定条目，不能像 ReplacePoster 那样按空路径批量命中。
func TestSetPosterOnlyUpdatesTarget(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	if _, err := db.Exec(`UPDATE items SET tmdb_id='2', image_path='' WHERE type='Movie'`); err != nil {
		t.Fatal(err)
	}

	rel := "images/0123456789abcdef01234567.jpg"
	if err := db.SetPoster("m1", rel); err != nil {
		t.Fatalf("SetPoster: %v", err)
	}

	var got string
	if err := db.QueryRow(`SELECT image_path FROM items WHERE id='m1'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != rel {
		t.Fatalf("m1 image_path = %q, want %q", got, rel)
	}
	var other int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE id<>'m1' AND image_path<>''`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if other != 0 {
		t.Fatalf("%d other items got the poster too", other)
	}
}
