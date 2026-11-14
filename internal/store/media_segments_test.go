package store

import "testing"

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
