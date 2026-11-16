package store

import (
	"testing"
	"time"
)

func TestReplaceMediaSegmentsKeepsManual(t *testing.T) {
	db := newTestDB(t)

	segs := []MediaSegment{
		{ItemID: "ep1", Type: SegmentIntro, StartTicks: 100, EndTicks: 200, Source: "chapter"},
		{ItemID: "ep1", Type: SegmentOutro, StartTicks: 900, EndTicks: 1000, Source: "chapter"},
	}
	if err := db.ReplaceMediaSegments("ep1", segs, "chapter", ""); err != nil {
		t.Fatal(err)
	}
	if done, err := db.SegmentAnalysisDone("ep1"); err != nil || !done {
		t.Fatalf("analysis done = %v, err = %v", done, err)
	}

	// 用户手动片段不应该被后续章节扫描覆盖。
	if _, err := db.Exec(`INSERT INTO media_segments
        (id, item_id, type, start_ticks, end_ticks, source, confidence, created_at, updated_at)
        VALUES ('manual1', 'ep1', 'Intro', 300, 400, 'manual', 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceMediaSegments("ep1", segs[:1], "chapter", ""); err != nil {
		t.Fatal(err)
	}
	got, err := db.MediaSegmentsForItem("ep1", nil)
	if err != nil {
		t.Fatal(err)
	}
	var manual, chapter int
	for _, s := range got {
		switch s.Source {
		case "manual":
			manual++
		case "chapter":
			chapter++
		}
	}
	if manual != 1 || chapter != 1 {
		t.Fatalf("manual=%d chapter=%d, want 1/1: %+v", manual, chapter, got)
	}

	// 类型过滤
	onlyIntro, err := db.MediaSegmentsForItem("ep1", []string{SegmentIntro})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyIntro) != 2 {
		t.Fatalf("intro segments = %d, want 2", len(onlyIntro))
	}
}

func TestFingerprintCandidatesSkipAttemptedAndCoveredSeasons(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	if err := db.SetFavorite("u1", "s1", true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2"} {
		if err := db.UpsertMediaSource(&MediaSource{
			ID: "src-" + id, ItemID: id, URL: "http://example/" + id, ProbeStatus: "done",
		}); err != nil {
			t.Fatal(err)
		}
	}

	cands, err := db.FingerprintCandidates("favorites", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].SeriesID != "s1" || cands[0].Season != 1 || cands[0].Episodes != 2 {
		t.Fatalf("candidates = %+v, want s1 season 1 with 2 episodes", cands)
	}

	// 记录一次尝试后，7 天重试窗口内不再重复扫。
	if err := db.MarkFingerprintRun("s1", 1, "done", "saved=0"); err != nil {
		t.Fatal(err)
	}
	cands, err = db.FingerprintCandidates("favorites", 0, time.Now().Add(-time.Hour).Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates after attempt = %+v, want none", cands)
	}

	// 已经有 Intro/Outro 的季不需要指纹。
	if _, err := db.Exec(`DELETE FROM segment_fingerprint_runs`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceMediaSegments("e1", []MediaSegment{{
		ItemID: "e1", Type: SegmentIntro, StartTicks: 100, EndTicks: 200, Source: "chapter",
	}}, "chapter", ""); err != nil {
		t.Fatal(err)
	}
	cands, err = db.FingerprintCandidates("favorites", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates with existing segments = %+v, want none", cands)
	}
}

func TestFingerprintCandidatesActiveNeedsTenMinutes(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	for _, id := range []string{"e1", "e2"} {
		if err := db.UpsertMediaSource(&MediaSource{
			ID: "src-" + id, ItemID: id, URL: "http://example/" + id, ProbeStatus: "done",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 只看了 5 分钟：老剧不进入自动回填。
	if err := db.UpsertPlaystate("u1", "e1", 5*60*10_000_000, false); err != nil {
		t.Fatal(err)
	}
	cands, err := db.FingerprintCandidates("active", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("5min candidates = %+v, want none", cands)
	}

	// 看到 10 分钟以上：整季进入回填。
	if err := db.UpsertPlaystate("u1", "e1", 10*60*10_000_000, false); err != nil {
		t.Fatal(err)
	}
	cands, err = db.FingerprintCandidates("active", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].SeriesID != "s1" || cands[0].Season != 1 {
		t.Fatalf("10min candidates = %+v, want s1 season 1", cands)
	}
}

// 回填顺序：没看过 → 在看 → 已完成。
func TestSegmentBackfillOrder(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	if err := db.UpsertItem(&Item{
		ID: "e3", LibraryID: "lib", ParentID: "se1", Type: TypeEpisode,
		Name: "第 3 集", SeriesID: "s1", SeasonID: "se1",
		IndexNumber: 3, ParentIndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if err := db.UpsertMediaSource(&MediaSource{
			ID: "src-" + id, ItemID: id, URL: "http://example/" + id, ProbeStatus: "done",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// e1 已看完，e3 看到一半，e2 没看过。
	if err := db.UpsertPlaystate("u1", "e1", 1000, true); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaystate("u1", "e3", 500, false); err != nil {
		t.Fatal(err)
	}

	srcs, err := db.SourcesForSeriesSegmentBackfill("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(srcs))
	for _, s := range srcs {
		got = append(got, s.ItemID)
	}
	want := []string{"e2", "e3", "e1"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// 实时入库触发只应该拿到指定条目的主片源，不把整库带进来。
func TestPrimarySourcesForItems(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-e1-a", ItemID: "e1", URL: "http://example/e1-a", StrmPath: "/e1-a.strm", SortRank: 0, Size: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-e1-b", ItemID: "e1", URL: "http://example/e1-b", StrmPath: "/e1-b.strm", SortRank: 1, Size: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-e2", ItemID: "e2", URL: "http://example/e2", StrmPath: "/e2.strm", SortRank: 0, Size: 100,
	}); err != nil {
		t.Fatal(err)
	}

	srcs, err := db.PrimarySourcesForItems([]string{"e1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].ID != "src-e1-a" {
		t.Fatalf("sources = %+v, want only src-e1-a", srcs)
	}
}
