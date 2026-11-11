package store

import (
	"testing"
	"time"
)

func seedSearchLibrary(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','mixed',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatalf("seed library: %v", err)
	}
}

func TestSearchIndex(t *testing.T) {
	db := newTestDB(t)
	seedSearchLibrary(t, db)
	rebuilt, err := db.EnsureSearchIndex()
	if err != nil {
		t.Fatalf("ensure search index: %v", err)
	}
	if !rebuilt {
		t.Fatal("first EnsureSearchIndex should rebuild")
	}
	if rebuilt, err = db.EnsureSearchIndex(); err != nil || rebuilt {
		t.Fatalf("second EnsureSearchIndex rebuilt=%v err=%v, want false/nil", rebuilt, err)
	}

	items := []*Item{
		{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "流浪地球", SortName: "流浪地球", OriginalTitle: "The Wandering Earth"},
		{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "流浪地球2", SortName: "流浪地球2"},
		{ID: "m3", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "三体", SortName: "三体"},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert %s: %v", it.ID, err)
		}
	}

	search := func(term string) []string {
		t.Helper()
		got, total, err := db.QueryItems(ItemQuery{SearchTerm: term})
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		ids := make([]string, 0, len(got))
		for _, it := range got {
			ids = append(ids, it.ID)
		}
		if total != len(ids) {
			t.Fatalf("search %q: total=%d len=%d", term, total, len(ids))
		}
		return ids
	}

	// 3 个字符以上走 FTS 子串匹配（不是前缀匹配）。
	if got := search("流浪地球"); len(got) != 2 {
		t.Fatalf("fts search 流浪地球 = %v, want 2 items", got)
	}
	// 2 个字符退回 LIKE。
	if got := search("流浪"); len(got) != 2 {
		t.Fatalf("like search 流浪 = %v, want 2 items", got)
	}
	// 名字中间的 2 字也要能命中（bigram 索引，不是前缀匹配）。
	if got := search("地球"); len(got) != 2 {
		t.Fatalf("bigram search 地球 = %v, want 2 items", got)
	}
	if got := search("三体"); len(got) != 1 || got[0] != "m3" {
		t.Fatalf("like search 三体 = %v, want [m3]", got)
	}
	// 英文原名。
	if got := search("wandering"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("fts search wandering = %v, want [m1]", got)
	}

	// 改名后触发器要同步索引。
	m1 := items[0]
	m1.Name = "流浪地球：重制版"
	m1.SortName = m1.Name
	if err := db.UpsertItem(m1); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := search("重制版"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("search after rename = %v, want [m1]", got)
	}
	if got := search("重制"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("bigram search after rename = %v, want [m1]", got)
	}

	// 删除后不应再命中。
	if err := db.DeleteItem("m2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := search("流浪地球2"); len(got) != 0 {
		t.Fatalf("search after delete = %v, want empty", got)
	}
}

// 索引不可用时（例如建表失败）仍应退回 LIKE，不能把搜索变成空结果。
func TestSearchFallbackWithoutIndex(t *testing.T) {
	db := newTestDB(t)
	seedSearchLibrary(t, db)
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "流浪地球", SortName: "流浪地球",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, _, err := db.QueryItems(ItemQuery{SearchTerm: "流浪地球"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("fallback search = %v, want [m1]", got)
	}
}
