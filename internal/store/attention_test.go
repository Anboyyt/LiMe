package store

import "testing"

func TestAttentionLists(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 初始：剧 s1 和电影 m1 都没有 TMDB ID。
	noTmdb, err := db.ItemsMissingTmdbID(50)
	if err != nil || len(noTmdb) != 2 {
		t.Fatalf("missing tmdb = %+v %v", noTmdb, err)
	}

	// 造两个同 TMDB ID 的电影，应该被识别为重复。
	m1, _ := db.GetItem("m1")
	m1.TmdbID = "999"
	if err := db.UpsertItem(m1); err != nil {
		t.Fatal(err)
	}
	m2 := &Item{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "电影 2", SortName: "电影 2", TmdbID: "999"}
	if err := db.UpsertItem(m2); err != nil {
		t.Fatal(err)
	}
	groups, err := db.DuplicateTmdbGroups(10)
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 2 {
		t.Fatalf("duplicates = %+v %v", groups, err)
	}

	art, err := db.ItemsMissingArtwork(50)
	if err != nil || len(art) == 0 {
		t.Fatalf("missing artwork = %+v %v", art, err)
	}
	if !art[0].MissingPoster || !art[0].MissingBackdrop {
		t.Fatalf("artwork flags = %+v", art[0])
	}
}

func TestTaskRunHistory(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	_ = db.RecordTaskRun("scan", 100, 110, "ok", "扫描 1 条")
	_ = db.RecordTaskRun("scrape", 200, 230, "error", "接口错误")
	_ = db.RecordTaskRun("backup", 300, 305, "ok", "备份")

	runs, err := db.TaskRuns(2)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	if runs[0].Kind != "backup" || runs[1].Kind != "scrape" {
		t.Fatalf("order = %+v", runs)
	}
}

func TestQueryItemsScrapeState(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	items, total, err := db.QueryItems(ItemQuery{Types: []string{TypeMovie}, ScrapeState: "none"})
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("none = %+v total=%d err=%v", items, total, err)
	}

	m1, _ := db.GetItem("m1")
	m1.TmdbID = "123"
	m1.ImagePath = "images/p.jpg"
	m1.BackdropPath = "images/b.jpg"
	if err := db.UpsertItem(m1); err != nil {
		t.Fatal(err)
	}
	if _, total, err := db.QueryItems(ItemQuery{Types: []string{TypeMovie}, ScrapeState: "missing_poster"}); err != nil || total != 0 {
		t.Fatalf("missing_poster total = %d err=%v", total, err)
	}
}
