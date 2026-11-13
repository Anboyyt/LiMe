package store

import "testing"

// 修改单集时要把所属剧集的演员一并刷新，否则新增剧的演员永远进不了索引。
func TestIndexPersonsFromItemsIncludesSeries(t *testing.T) {
	db := newTestDB(t)
	lib, err := db.UpsertLibrary("剧集", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []*Item{
		{ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: TypeSeries, Name: "剧", SortName: "剧",
			People: `[{"Name":"本地演员","Type":"Actor"}]`},
		{ID: "e1", LibraryID: lib.ID, ParentID: "s1", Type: TypeEpisode, Name: "第1集", SortName: "0001",
			SeriesID: "s1"},
	} {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.IndexPersonsFromItems([]string{"e1"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("indexed %d persons, want 1", n)
	}
	names, err := db.PersonNamesByIDs([]string{PersonID("本地演员")})
	if err != nil {
		t.Fatal(err)
	}
	if names[PersonID("本地演员")] != "本地演员" {
		t.Fatalf("names = %v", names)
	}
}

func TestPersonsBackfillAndFilter(t *testing.T) {
	db := newTestDB(t)
	lib, err := db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}

	items := []*Item{
		{ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: TypeMovie, Name: "m1", SortName: "m1",
			People: `[{"Name":"Tom Hanks","Role":"主角","Type":"Actor"}]`},
		{ID: "m2", LibraryID: lib.ID, ParentID: lib.ID, Type: TypeMovie, Name: "m2", SortName: "m2",
			People: `[{"Name":"无头像","Role":"配角","Type":"Actor"}]`},
		{ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: TypeSeries, Name: "s1", SortName: "s1",
			People: `[{"Name":"Tom Hanks","Role":"主角","Type":"Actor"},{"Name":"无头像","Type":"Actor"}]`},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.BackfillPersonsFromItems()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("backfilled %d persons, want 2", n)
	}
	// 第二次调用不应重复写入。
	if n, err := db.BackfillPersonsFromItems(); err != nil || n != 0 {
		t.Fatalf("second backfill = %d, %v", n, err)
	}

	ids := []string{PersonID("Tom Hanks"), PersonID("无头像"), "missing"}
	names, err := db.PersonNamesByIDs(ids)
	if err != nil {
		t.Fatal(err)
	}
	if names[PersonID("Tom Hanks")] != "Tom Hanks" || names[PersonID("无头像")] != "无头像" {
		t.Fatalf("names = %v", names)
	}
	if _, ok := names["missing"]; ok {
		t.Fatal("unknown id should not resolve")
	}

	got, total, err := db.QueryItems(ItemQuery{Persons: []string{"tom hanks"}, Types: []string{TypeMovie}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("person filter = %d %v, want m1", total, got)
	}

	got, total, err = db.QueryItems(ItemQuery{Persons: []string{"无头像"}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("无头像 total = %d, want 2", total)
	}

	list, total, err := db.ListPersons("tom", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Name != "Tom Hanks" {
		t.Fatalf("list persons = %d %v", total, list)
	}
}

func TestPersonImages(t *testing.T) {
	db := newTestDB(t)

	id := PersonID("Tom Hanks")
	if id != PersonID("  tom hanks ") {
		t.Fatal("person id should be case/space insensitive")
	}
	if len(id) != 32 {
		t.Fatalf("id length = %d, want 32", len(id))
	}

	if err := db.UpsertPersonImages([]PersonImage{
		{ID: id, Name: "Tom Hanks", ImageURL: "https://example.com/tom.jpg"},
		{ID: PersonID("无头像"), Name: "无头像"},
	}); err != nil {
		t.Fatal(err)
	}

	byID, err := db.PersonImageByID(id)
	if err != nil || byID.Name != "Tom Hanks" || byID.ImageURL == "" {
		t.Fatalf("by id = %+v, %v", byID, err)
	}
	byName, err := db.PersonImageByName("Tom Hanks")
	if err != nil || byName.ID != id {
		t.Fatalf("by name = %+v, %v", byName, err)
	}
	if _, err := db.PersonImageByID("missing"); err != ErrNotFound {
		t.Fatalf("missing err = %v", err)
	}
}
