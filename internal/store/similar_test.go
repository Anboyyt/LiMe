package store

import "testing"

// seedSimilarFixture 建 4 部电影 + 1 部剧集，类型标签和演员交错，
// 用来验证相似推荐按「类型交集 → 同演员 → 年份」排序，且不跨类型。
func seedSimilarFixture(t *testing.T, db *DB) {
	t.Helper()
	seedPlaybackFixture(t, db)

	extra := []*Item{
		{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "同类型同演员", SortName: "m2"},
		{ID: "m3", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "同类型早年", SortName: "m3"},
		{ID: "m4", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "完全不同", SortName: "m4"},
	}
	for _, it := range extra {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert %s: %v", it.ID, err)
		}
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`UPDATE items SET genres='["动作"]', people='[{"Name":"张三"}]', production_year=2020 WHERE id='m1'`, nil},
		{`UPDATE items SET genres='["动作","科幻"]', people='[{"Name":"张三"}]', production_year=2020 WHERE id='m2'`, nil},
		{`UPDATE items SET genres='["动作"]', people='[]', production_year=2010 WHERE id='m3'`, nil},
		{`UPDATE items SET genres='["喜剧"]', people='[{"Name":"李四"}]', production_year=2020 WHERE id='m4'`, nil},
		{`UPDATE items SET genres='["动作","科幻"]', people='[{"Name":"张三"}]', production_year=2020 WHERE id='s1'`, nil},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed similar: %v", err)
		}
	}
}

func TestSimilarItemsRanksAndStaysInType(t *testing.T) {
	db := newTestDB(t)
	seedSimilarFixture(t, db)

	items, err := db.SimilarItems("m1", 10)
	if err != nil {
		t.Fatalf("SimilarItems: %v", err)
	}
	// m4 类型不同、s1 是剧集，都不该出现；m1 是自身也要排除。
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), ids(items))
	}
	if items[0].ID != "m2" {
		t.Fatalf("first = %s, want m2（类型交集 2 个 + 同演员）: %v", items[0].ID, ids(items))
	}
	if items[1].ID != "m3" {
		t.Fatalf("second = %s, want m3: %v", items[1].ID, ids(items))
	}

	// 剧集只推剧集：s1 没有同类剧集，应返回空而不是电影。
	series, err := db.SimilarItems("s1", 10)
	if err != nil {
		t.Fatalf("SimilarItems(series): %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("series got %v, want none", ids(series))
	}
}

// 单集/季按所属剧集推荐，结果仍是剧集。
func TestSimilarItemsEpisodeUsesSeries(t *testing.T) {
	db := newTestDB(t)
	seedSimilarFixture(t, db)

	items, err := db.SimilarItems("e1", 10)
	if err != nil {
		t.Fatalf("SimilarItems(episode): %v", err)
	}
	for _, it := range items {
		if it.Type != TypeSeries {
			t.Fatalf("episode similar returned %s (%s)", it.ID, it.Type)
		}
	}
}

// 没有类型标签的条目退回「最近入库的同类型」，不能返回空。
func TestSimilarItemsFallsBackWithoutGenres(t *testing.T) {
	db := newTestDB(t)
	seedSimilarFixture(t, db)
	if _, err := db.Exec(`UPDATE items SET genres='' WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}

	items, err := db.SimilarItems("m1", 10)
	if err != nil {
		t.Fatalf("SimilarItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %v, want 3 movies", ids(items))
	}
	for _, it := range items {
		if it.Type != TypeMovie || it.ID == "m1" {
			t.Fatalf("bad fallback item %s (%s)", it.ID, it.Type)
		}
	}
}

func ids(items []*Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}
