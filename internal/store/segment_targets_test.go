package store

import (
	"testing"
	"time"
)

func seedSourcesForFingerprint(t *testing.T, db *DB) {
	t.Helper()
	now := time.Now().Unix()
	rows := []struct {
		id   string
		item string
		size int64
		rank int
	}{
		{"ms-big", "e1", 1000, 0},
		{"ms-small", "e1", 100, 1},
		{"ms-e2", "e2", 500, 0},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO media_sources
            (id, item_id, url, size, sort_rank, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.id, r.item, "http://example/"+r.id, r.size, r.rank, now); err != nil {
			t.Fatalf("seed source %s: %v", r.id, err)
		}
	}
}

func TestSeasonSourcesPreferLowBitrate(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	seedSourcesForFingerprint(t, db)

	// 默认按 sort_rank + 体积降序，取到 ms-big。
	srcs, err := db.SeasonSources("s1", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 || srcs[0].URL != "http://example/ms-big" {
		t.Fatalf("default sources = %+v, want ms-big first", srcs)
	}

	// 低码率优先取体积最小的 ms-small（尽管 sort_rank 更靠后）。
	srcs, err = db.SeasonSources("s1", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 || srcs[0].URL != "http://example/ms-small" {
		t.Fatalf("low-bitrate sources = %+v, want ms-small first", srcs)
	}
}

func TestSegmentTargetsQueue(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	seedSourcesForFingerprint(t, db)

	seasons, err := db.SeasonsNeedingSegments("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 1 || seasons[0] != 1 {
		t.Fatalf("seasons = %v, want [1]", seasons)
	}

	added, err := db.AddSegmentTargets([]SegmentTarget{{SeriesID: "s1", Season: 1}})
	if err != nil || added != 1 {
		t.Fatalf("add = %d, %v; want 1", added, err)
	}
	counts, err := db.SegmentTargetCounts()
	if err != nil || counts["queued"] != 1 {
		t.Fatalf("counts = %v, %v; want queued=1", counts, err)
	}

	target, err := db.NextQueuedSegmentTarget()
	if err != nil || target == nil || target.SeriesID != "s1" || target.Season != 1 {
		t.Fatalf("next = %+v, %v", target, err)
	}
	if err := db.SetSegmentTargetStatus("s1", 1, "done", "saved=2"); err != nil {
		t.Fatal(err)
	}
	if target, err = db.NextQueuedSegmentTarget(); err != nil || target != nil {
		t.Fatalf("next after done = %+v, %v; want nil", target, err)
	}
}

func TestSearchSeries(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	hits, err := db.SearchSeries("剧", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "s1" || hits[0].Seasons != 1 || hits[0].Episodes != 2 {
		t.Fatalf("hits = %+v, want s1 with 1 season / 2 episodes", hits)
	}
	if hits, err := db.SearchSeries("%", 10); err != nil || len(hits) != 0 {
		t.Fatalf("wildcard should be literal: %+v, %v", hits, err)
	}
}
