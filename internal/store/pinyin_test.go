package store

import (
	"testing"
	"time"
)

func TestPinyinSortAndSearch(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	if _, err := db.Exec(`INSERT INTO libraries (id,name,path,kind,created_at) VALUES ('lib','测试','/strm','mixed',?)`, now.Unix()); err != nil {
		t.Fatal(err)
	}

	mustItem := func(it *Item) {
		t.Helper()
		it.CreatedAt = now
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert %s: %v", it.Name, err)
		}
	}
	mustItem(&Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "三体", SortName: "三体", SortKey: "santi", Pinyin: "santi", PinyinAbbr: "st"})
	mustItem(&Item{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "流浪地球", SortName: "流浪地球", SortKey: "liulangdiqiu", Pinyin: "liulangdiqiu", PinyinAbbr: "lldq"})
	mustItem(&Item{ID: "m3", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "Spider-Man", SortName: "spider-man"})

	if _, err := db.EnsureSearchIndex(); err != nil {
		t.Fatalf("ensure search index: %v", err)
	}

	cases := []struct {
		term string
		want string
	}{
		{"st", "m1"},      // 首字母
		{"santi", "m1"},   // 全拼
		{"lldq", "m2"},    // 首字母
		{"liulang", "m2"}, // 全拼前缀
		{"spider", "m3"},  // 英文走 FTS，不应被拼音分支破坏
	}
	for _, c := range cases {
		got, _, err := db.QueryItems(ItemQuery{
			LibraryID:  "lib",
			Types:      []string{TypeMovie},
			SearchTerm: c.term,
		})
		if err != nil {
			t.Fatalf("search %q: %v", c.term, err)
		}
		if len(got) != 1 || got[0].ID != c.want {
			ids := make([]string, 0, len(got))
			for _, it := range got {
				ids = append(ids, it.ID)
			}
			t.Errorf("search %q = %v, want [%s]", c.term, ids, c.want)
		}
	}

	// 按 SortName 排序应走拼音键：liulangdiqiu < santi < spider-man。
	got, _, err := db.QueryItems(ItemQuery{
		LibraryID: "lib",
		Types:     []string{TypeMovie},
		SortBy:    []string{"SortName"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m2", "m1", "m3"}
	if len(got) != len(want) {
		t.Fatalf("sort got %d items", len(got))
	}
	for i := range want {
		if got[i].ID != want[i] {
			ids := make([]string, 0, len(got))
			for _, it := range got {
				ids = append(ids, it.ID)
			}
			t.Fatalf("sort order = %v, want %v", ids, want)
		}
	}
}

func TestBackfillPinyin(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	if _, err := db.Exec(`INSERT INTO libraries (id,name,path,kind,created_at) VALUES ('lib','测试','/strm','mixed',?)`, now); err != nil {
		t.Fatal(err)
	}
	// 模拟迁移前的老数据：sort_key 为空。
	if _, err := db.Exec(`INSERT INTO items (id,library_id,parent_id,type,name,original_title,sort_name,created_at,updated_at)
		VALUES ('old','lib','lib','Movie','三体','','三体',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}

	n, err := db.BackfillPinyin()
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled %d rows, want 1", n)
	}
	it, err := db.GetItem("old")
	if err != nil {
		t.Fatal(err)
	}
	if it.SortKey != "santi" || it.Pinyin != "santi" || it.PinyinAbbr != "st" {
		t.Fatalf("sort_key=%q pinyin=%q abbr=%q", it.SortKey, it.Pinyin, it.PinyinAbbr)
	}
}
