package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

// 老库里单集可能没写 SeriesID，只有 Season 上有。这种情况下必须用所属季
// 反推出剧集名，否则客户端会把 SeriesName 渲染成字面量 "null"，文件名
// 变成 null.S01E01。
func TestEpisodeSeriesNameFallsBackToSeason(t *testing.T) {
	env, user, tok := newAuthedEnv(t)

	lib, err := env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []*store.Item{
		{ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries, Name: "My Show"},
		{ID: "se1", LibraryID: lib.ID, ParentID: "s1", Type: store.TypeSeason, Name: "第 1 季", SeriesID: "s1"},
		// 关键：SeriesID 留空，只有 ParentID 指向季。
		{ID: "e1", LibraryID: lib.ID, ParentID: "se1", Type: store.TypeEpisode, Name: "第 1 集",
			SeasonID: "se1", ParentIndexNumber: 1, IndexNumber: 1},
	} {
		if err := env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	f := &userFixture{env: env, bob: user, bobTok: tok}
	rr := f.do(http.MethodGet, "/Items/e1", nil, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["SeriesName"] != "My Show" {
		t.Errorf("SeriesName = %v, want My Show", got["SeriesName"])
	}
	if got["SeriesId"] != "s1" {
		t.Errorf("SeriesId = %v, want s1", got["SeriesId"])
	}
	if got["SeasonName"] != "第 1 季" {
		t.Errorf("SeasonName = %v, want 第 1 季", got["SeasonName"])
	}
}
