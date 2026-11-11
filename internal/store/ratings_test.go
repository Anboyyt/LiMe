package store

import "testing"

func TestRatingScore(t *testing.T) {
	cases := map[string]int{
		"":       0,
		"G":      1,
		"TV-Y":   1,
		"PG":     2,
		"TV-PG":  2,
		"PG-13":  3,
		"TV-14":  3,
		"12":     3,
		"R":      4,
		"FSK 16": 4,
		"NC-17":  5,
		"TV-MA":  5,
		"18+":    5,
		"R18":    5,
		"random": 0,
		"全年龄":    1,
	}
	for rating, want := range cases {
		if got := RatingScore(rating); got != want {
			t.Errorf("RatingScore(%q) = %d, want %d", rating, got, want)
		}
	}
}

// 家长控制查询：未分级放行，超限过滤。
func TestQueryItemsMaxRating(t *testing.T) {
	db := newTestDB(t)
	lib, err := db.CreateLibrary("电影", []string{"/strm/movies"}, "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []*Item{
		{ID: "g", LibraryID: lib.ID, Type: TypeMovie, Name: "G", OfficialRating: "G"},
		{ID: "pg13", LibraryID: lib.ID, Type: TypeMovie, Name: "PG13", OfficialRating: "PG-13"},
		{ID: "r", LibraryID: lib.ID, Type: TypeMovie, Name: "R", OfficialRating: "R"},
		{ID: "none", LibraryID: lib.ID, Type: TypeMovie, Name: "None"},
	} {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateItemRating(it.ID, it.OfficialRating); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := db.QueryItems(ItemQuery{Types: []string{TypeMovie}, MaxRating: 3})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	for _, it := range items {
		if it.ID == "r" {
			t.Fatal("R-rated item should be filtered")
		}
	}
}
