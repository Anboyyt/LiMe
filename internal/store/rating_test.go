package store

import "testing"

// 剧集分级要能继承给季和单集，已有分级不被覆盖。
func TestPropagateSeriesRatings(t *testing.T) {
	db := newTestDB(t)
	lib, err := db.CreateLibrary("剧集", []string{"/strm/tv"}, "tvshows")
	if err != nil {
		t.Fatal(err)
	}

	items := []*Item{
		{ID: "s1", LibraryID: lib.ID, Type: TypeSeries, Name: "S", OfficialRating: "TV-14"},
		{ID: "se1", LibraryID: lib.ID, Type: TypeSeason, Name: "S1", SeriesID: "s1"},
		{ID: "e1", LibraryID: lib.ID, Type: TypeEpisode, Name: "E1", SeriesID: "s1", SeasonID: "se1"},
		{ID: "e2", LibraryID: lib.ID, Type: TypeEpisode, Name: "E2", SeriesID: "s1", SeasonID: "se1", OfficialRating: "TV-MA"},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert %s: %v", it.ID, err)
		}
	}

	n, err := db.PropagateSeriesRatings()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("updated = %d, want 2", n)
	}

	for id, want := range map[string]string{"se1": "TV-14", "e1": "TV-14", "e2": "TV-MA"} {
		got, err := db.GetItem(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.OfficialRating != want {
			t.Fatalf("%s rating = %q, want %q", id, got.OfficialRating, want)
		}
	}
}
