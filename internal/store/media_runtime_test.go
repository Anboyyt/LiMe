package store

import (
	"testing"
	"time"
)

func TestFillItemRuntimeFromSources(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	if _, err := db.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','mixed',?)`,
		now.Unix(),
	); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	items := []*Item{
		// 空时长 + 两个已探测片源：取最大值
		{ID: "e1", LibraryID: "lib", ParentID: "lib", Type: TypeEpisode, Name: "第 1 集", SortName: "第 1 集", CreatedAt: now},
		// 已有刮削时长：不该被覆盖
		{ID: "e2", LibraryID: "lib", ParentID: "lib", Type: TypeEpisode, Name: "第 2 集", SortName: "第 2 集", RunTimeTicks: 9999 * 10_000_000, CreatedAt: now},
		// 空时长，但片源没探测成功：保持 0
		{ID: "e3", LibraryID: "lib", ParentID: "lib", Type: TypeEpisode, Name: "第 3 集", SortName: "第 3 集", CreatedAt: now},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert item %s: %v", it.ID, err)
		}
	}

	sources := []*MediaSource{
		{ID: "s1", ItemID: "e1", URL: "http://example/1", ProbeStatus: "done", DurationTicks: 1200 * 10_000_000},
		{ID: "s2", ItemID: "e1", URL: "http://example/2", ProbeStatus: "done", DurationTicks: 1210 * 10_000_000},
		{ID: "s3", ItemID: "e1", URL: "http://example/3", ProbeStatus: "failed", DurationTicks: 9000 * 10_000_000},
		{ID: "s4", ItemID: "e2", URL: "http://example/4", ProbeStatus: "done", DurationTicks: 1200 * 10_000_000},
		{ID: "s5", ItemID: "e3", URL: "http://example/5", ProbeStatus: "none"},
	}
	for _, s := range sources {
		if err := db.UpsertMediaSource(s); err != nil {
			t.Fatalf("upsert source %s: %v", s.ID, err)
		}
	}

	for _, id := range []string{"e1", "e2", "e3"} {
		if err := db.FillItemRuntimeFromSources(id); err != nil {
			t.Fatalf("fill %s: %v", id, err)
		}
	}

	want := map[string]int64{
		"e1": 1210 * 10_000_000, // 取已探测片源的最大值，忽略 failed
		"e2": 9999 * 10_000_000, // 已有值不覆盖
		"e3": 0,                 // 没有探测成功的片源
	}
	for id, ticks := range want {
		it, err := db.GetItem(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if it.RunTimeTicks != ticks {
			t.Fatalf("%s runtime = %d, want %d", id, it.RunTimeTicks, ticks)
		}
	}
}
