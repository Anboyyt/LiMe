package store

import (
	"testing"
	"time"
)

func TestSourcesForProbeWarmup(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db) // 有 s1/e1/e2 + m1，library=lib

	now := time.Now()
	// 新条目：应该被预热
	if err := db.UpsertItem(&Item{
		ID: "new1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "新电影", SortName: "新电影", CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-new", ItemID: "new1", URL: "http://example/1", ProbeStatus: "none",
	}); err != nil {
		t.Fatal(err)
	}
	// 老条目但有观看进度：应该被预热
	if _, err := db.Exec(`UPDATE items SET created_at = ? WHERE id = 'e1'`, now.Add(-90*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-progress", ItemID: "e1", URL: "http://example/2", ProbeStatus: "none",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaystate("u1", "e1", 1200, false); err != nil {
		t.Fatal(err)
	}
	// 老条目无进度：不该被预热
	if _, err := db.Exec(`UPDATE items SET created_at = ? WHERE id = 'e2'`, now.Add(-90*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-old", ItemID: "e2", URL: "http://example/3", ProbeStatus: "none",
	}); err != nil {
		t.Fatal(err)
	}
	// 已探测：不该再出现
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-done", ItemID: "m1", URL: "http://example/4", ProbeStatus: "done",
	}); err != nil {
		t.Fatal(err)
	}

	srcs, err := db.SourcesForProbeWarmup(now.Add(-7*24*time.Hour).Unix(), 10, "recent")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range srcs {
		got[s.ID] = true
	}
	if !got["src-new"] || !got["src-progress"] {
		t.Fatalf("missing warmup sources: %v", got)
	}
	if got["src-old"] || got["src-done"] {
		t.Fatalf("unexpected warmup sources: %v", got)
	}
}

// 收藏范围：收藏的电影/单集，以及收藏剧集下的单集，其他不选。
func TestSourcesForProbeWarmupFavorites(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db) // s1 + e1/e2 + m1

	for _, src := range []*MediaSource{
		{ID: "src-ep1", ItemID: "e1", URL: "http://example/e1", ProbeStatus: "none"},
		{ID: "src-ep2", ItemID: "e2", URL: "http://example/e2", ProbeStatus: "none"},
		{ID: "src-movie", ItemID: "m1", URL: "http://example/m1", ProbeStatus: "none"},
	} {
		if err := db.UpsertMediaSource(src); err != nil {
			t.Fatal(err)
		}
	}
	// 收藏 s1 -> e1/e2 都应入选；m1 未收藏不选
	if err := db.SetFavorite("u1", "s1", true); err != nil {
		t.Fatal(err)
	}
	srcs, err := db.SourcesForProbeWarmup(0, 10, "favorites")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range srcs {
		got[s.ID] = true
	}
	if !got["src-ep1"] || !got["src-ep2"] || got["src-movie"] {
		t.Fatalf("favorite scope wrong: %v", got)
	}

	// 全库范围（since=0, favoriteOnly=false）三个都在
	srcs, err = db.SourcesForProbeWarmup(0, 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 3 {
		t.Fatalf("all scope got %d, want 3", len(srcs))
	}
}

// 在追范围：某部剧有未看完进度，整部剧的单集都加入预热。
func TestSourcesForProbeWarmupWatching(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db) // s1 + e1/e2 + m1

	for _, src := range []*MediaSource{
		{ID: "src-ep1", ItemID: "e1", URL: "http://example/e1", ProbeStatus: "none"},
		{ID: "src-ep2", ItemID: "e2", URL: "http://example/e2", ProbeStatus: "none"},
		{ID: "src-movie", ItemID: "m1", URL: "http://example/m1", ProbeStatus: "none"},
	} {
		if err := db.UpsertMediaSource(src); err != nil {
			t.Fatal(err)
		}
	}
	// e1 有观看进度 -> s1 算在追
	if err := db.UpsertPlaystate("u1", "e1", 1200, false); err != nil {
		t.Fatal(err)
	}

	srcs, err := db.SourcesForProbeWarmup(0, 10, "watching")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range srcs {
		got[s.ID] = true
	}
	if !got["src-ep1"] || !got["src-ep2"] {
		t.Fatalf("watching scope missing series episodes: %v", got)
	}
	if got["src-movie"] {
		t.Fatalf("watching scope should not include unfavorited movie: %v", got)
	}

	// 只看收藏时，未收藏的剧集不应出现
	srcs, err = db.SourcesForProbeWarmup(0, 10, "favorites")
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 0 {
		t.Fatalf("favorites scope = %d items, want 0", len(srcs))
	}
}

func TestSourcesForSeriesWarm(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db) // s1 + e1/e2

	for _, src := range []*MediaSource{
		{ID: "s2", ItemID: "e2", URL: "http://example/2", ProbeStatus: "none"},
		{ID: "s1", ItemID: "e1", URL: "http://example/1", ProbeStatus: "none"},
	} {
		if err := db.UpsertMediaSource(src); err != nil {
			t.Fatal(err)
		}
	}
	srcs, err := db.SourcesForSeriesWarm("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 || srcs[0].ID != "s1" || srcs[1].ID != "s2" {
		t.Fatalf("order = %v", srcs)
	}

	// 探测过的要排除
	if _, err := db.Exec(`UPDATE media_sources SET probe_status='done' WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	srcs, err = db.SourcesForSeriesWarm("s1", 10)
	if err != nil || len(srcs) != 1 || srcs[0].ID != "s2" {
		t.Fatalf("after done = %v, %v", srcs, err)
	}
}
