package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

// Web UI 的剧集卡片需要用剧集海报（2:3），而不是单集自己的 16:9 截图。
func TestEpisodeIncludesSeriesPoster(t *testing.T) {
	env, _, tok := newAuthedEnv(t)

	lib, err := env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "s1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeSeries, Name: "My Show", SortName: "my show",
		ImagePath: "posters/s1.jpg",
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

	req := httptest.NewRequest(http.MethodGet,
		"/emby/Items?Recursive=true&IncludeItemTypes=Episode&Limit=10", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v, body = %s", err, rr.Body.String())
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	ep := got.Items[0]
	wantTag := imageTag("posters/s1.jpg")
	if ep.SeriesPrimaryImageTag != wantTag {
		t.Errorf("SeriesPrimaryImageTag = %q, want %q", ep.SeriesPrimaryImageTag, wantTag)
	}
	if ep.SeriesPrimaryImageItemId != "s1" {
		t.Errorf("SeriesPrimaryImageItemId = %q, want s1", ep.SeriesPrimaryImageItemId)
	}
	if ep.SeriesName != "My Show" {
		t.Errorf("SeriesName = %q, want My Show", ep.SeriesName)
	}
}
