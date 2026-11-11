package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lime/internal/store"
)

func newAuthedEnv(t *testing.T) (*testEnv, *store.User, string) {
	t.Helper()
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	user, err := env.db.CreateUser("viewer", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(user.ID, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return env, user, tok
}

func getLatest(t *testing.T, env *testEnv, userID, tok, query string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/emby/Users/"+userID+"/Items/Latest"+query, nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// /Items/Latest 必须是裸数组，不是 QueryResult。
	var items []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("expected JSON array, got: %s", rr.Body.String())
	}
	return items
}

func TestLatestItemsScopedToParentAndReturnsArray(t *testing.T) {
	env, user, tok := newAuthedEnv(t)

	libA, err := env.db.UpsertLibrary("A", "/strm/a", "movies")
	if err != nil {
		t.Fatal(err)
	}
	libB, err := env.db.UpsertLibrary("B", "/strm/b", "movies")
	if err != nil {
		t.Fatal(err)
	}

	items := []struct {
		id, lib, name string
	}{
		{"a1", libA.ID, "A One"},
		{"a2", libA.ID, "A Two"},
		{"b1", libB.ID, "B One"},
	}
	for _, it := range items {
		if err := env.db.UpsertItem(&store.Item{
			ID:        it.id,
			LibraryID: it.lib,
			ParentID:  it.lib,
			Type:      store.TypeMovie,
			Name:      it.name,
			SortName:  it.name,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := getLatest(t, env, user.ID, tok, "?ParentId="+libA.ID+"&Limit=10")
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	for _, it := range got {
		if name, _ := it["Name"].(string); name != "A One" && name != "A Two" {
			t.Errorf("leaked item from another library: %v", it["Name"])
		}
	}
}

func TestLatestItemsEpisodeIncludesSeriesName(t *testing.T) {
	env, user, tok := newAuthedEnv(t)

	lib, err := env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "s1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeSeries, Name: "My Show", SortName: "my show",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "se1", LibraryID: lib.ID, ParentID: "s1",
		Type: store.TypeSeason, Name: "Season 1", SortName: "season 1",
		SeriesID: "s1", ParentIndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "e1", LibraryID: lib.ID, ParentID: "se1",
		Type: store.TypeEpisode, Name: "第 1 集", SortName: "0001",
		SeriesID: "s1", SeasonID: "se1", ParentIndexNumber: 1, IndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// 显式要单集时才返回单集；默认（剧集库）应返回剧集卡片。
	got := getLatest(t, env, user.ID, tok,
		"?ParentId="+lib.ID+"&IncludeItemTypes=Episode&Limit=10")
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	if got[0]["Type"] != "Episode" {
		t.Errorf("type = %v, want Episode", got[0]["Type"])
	}
	if got[0]["SeriesName"] != "My Show" {
		t.Errorf("SeriesName = %v, want My Show", got[0]["SeriesName"])
	}
}

// 剧集库的「最新」必须一部剧一张卡，不能按单集铺开。
func TestLatestItemsTVReturnsSeries(t *testing.T) {
	env, user, tok := newAuthedEnv(t)

	lib, err := env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now().Add(-time.Hour)
	addSeries := func(id, name string) {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeSeries, Name: name, SortName: name,
			CreatedAt: base,
		}); err != nil {
			t.Fatal(err)
		}
	}
	addEpisode := func(id, series string, created time.Time) {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: series,
			Type: store.TypeEpisode, Name: id, SortName: id,
			SeriesID: series, CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}

	addSeries("sA", "Show A")
	addSeries("sB", "Show B")
	addEpisode("a1", "sA", base.Add(10*time.Second))
	addEpisode("a2", "sA", base.Add(20*time.Second))
	addEpisode("b1", "sB", base.Add(30*time.Second))

	got := getLatest(t, env, user.ID, tok, "?ParentId="+lib.ID+"&Limit=10")
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2 series", len(got))
	}
	for _, it := range got {
		if it["Type"] != "Series" {
			t.Errorf("type = %v, want Series", it["Type"])
		}
	}
	// 最新单集时间更晚的 B 应排在前面。
	if got[0]["Name"] != "Show B" {
		t.Errorf("first = %v, want Show B", got[0]["Name"])
	}
}
