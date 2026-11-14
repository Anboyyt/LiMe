package store

import "testing"

func TestSortByLatestEpisode(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db) // 已有 s1 + e1/e2

	// s1 的最新一集是 2025-02-01
	if _, err := db.Exec(`UPDATE items SET premiere_date = '2025-01-01' WHERE id = 'e1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET premiere_date = '2025-02-01' WHERE id = 'e2'`); err != nil {
		t.Fatal(err)
	}

	// 新剧 s2 的最新一集是 2026-01-01，应该排在前面
	if err := db.UpsertItem(&Item{
		ID: "s2", LibraryID: "lib", ParentID: "lib", Type: TypeSeries, Name: "新剧", SortName: "新剧",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&Item{
		ID: "e21", LibraryID: "lib", ParentID: "s2", Type: TypeEpisode, Name: "第 1 集",
		SeriesID: "s2", SeasonID: "s2se1", IndexNumber: 1, ParentIndexNumber: 1,
		PremiereDate: "2026-01-01",
	}); err != nil {
		t.Fatal(err)
	}

	items, total, err := db.QueryItems(ItemQuery{
		Types:  []string{TypeSeries},
		SortBy: []string{"LatestEpisode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d items=%d, want 2", total, len(items))
	}
	if items[0].ID != "s2" || items[1].ID != "s1" {
		t.Fatalf("order = %s, %s; want s2, s1", items[0].ID, items[1].ID)
	}
}

// 客户端的 DateLastContentAdded 要按「最新一集的入库时间」排，
// 而不是剧集自身 created_at（整库扫描后两者可能全部相同）。
func TestSortByDateLastContentAdded(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 两部剧自身 created_at 相同（模拟整库扫描），靠单集时间区分
	if _, err := db.Exec(`UPDATE items SET created_at = 1000 WHERE type = 'Series'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET created_at = 900 WHERE id IN ('e1','e2')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&Item{
		ID: "s2", LibraryID: "lib", ParentID: "lib", Type: TypeSeries, Name: "新剧", SortName: "新剧",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&Item{
		ID: "e21", LibraryID: "lib", ParentID: "s2", Type: TypeEpisode, Name: "第 1 集",
		SeriesID: "s2", SeasonID: "s2se1", IndexNumber: 1, ParentIndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET created_at = 1000 WHERE id = 's2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET created_at = 2000 WHERE id = 'e21'`); err != nil {
		t.Fatal(err)
	}

	for _, sortBy := range []string{"DateLastContentAdded", "DateLastMediaAdded"} {
		items, _, err := db.QueryItems(ItemQuery{
			Types:      []string{TypeSeries},
			SortBy:     []string{sortBy},
			Descending: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].ID != "s2" || items[1].ID != "s1" {
			t.Fatalf("%s order = %v, want s2, s1", sortBy, itemIDs(items))
		}
	}
}

func itemIDs(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestSortByProductionYear(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpsertItem(&Item{
		ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "老电影", SortName: "老电影", ProductionYear: 1999,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET production_year = 2020 WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}

	items, _, err := db.QueryItems(ItemQuery{
		Types:      []string{TypeMovie},
		SortBy:     []string{"ProductionYear"},
		Descending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "m1" || items[1].ID != "m2" {
		t.Fatalf("order = %v, want m1 (2020), m2 (1999)", itemIDs(items))
	}
}

// 同一年内必须严格按开播/上映日期排，不能退化成索引扫描顺序。
func TestSortByProductionYearWithinYearByPremiereDate(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	for _, m := range []struct {
		id   string
		name string
		date string
	}{
		{"m1", "一月", "2024-01-05"},
		{"m2", "腊月", "2024-12-20"},
		{"m3", "六月", "2024-06-15"},
	} {
		if err := db.UpsertItem(&Item{
			ID: m.id, LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
			Name: m.name, SortName: m.name, ProductionYear: 2024, PremiereDate: m.date,
		}); err != nil {
			t.Fatal(err)
		}
	}

	items, _, err := db.QueryItems(ItemQuery{
		Types:      []string{TypeMovie},
		SortBy:     []string{"ProductionYear"},
		Descending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(items); len(got) != 3 || got[0] != "m2" || got[1] != "m3" || got[2] != "m1" {
		t.Fatalf("order = %v, want m2 (12-20), m3 (06-15), m1 (01-05)", got)
	}
}

// 客户端直接发 SortBy=PremiereDate 时不能退化成按名称排。
func TestSortByPremiereDate(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	for _, m := range []struct {
		id   string
		name string
		date string
	}{
		{"m1", "老片", "1999-01-01"},
		{"m2", "新片", "2025-08-01"},
	} {
		if err := db.UpsertItem(&Item{
			ID: m.id, LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
			Name: m.name, SortName: m.name, PremiereDate: m.date,
		}); err != nil {
			t.Fatal(err)
		}
	}

	items, _, err := db.QueryItems(ItemQuery{
		Types:      []string{TypeMovie},
		SortBy:     []string{"PremiereDate"},
		Descending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(items); len(got) != 2 || got[0] != "m2" || got[1] != "m1" {
		t.Fatalf("order = %v, want m2 (2025), m1 (1999)", got)
	}
}
