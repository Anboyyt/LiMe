package store

import "testing"

// 归并键改成番号后，旧条目会变成没有片源的空壳；播放记录必须迁到
// 同番号的新条目上，否则续播进度会丢。
func TestReassignOrphanPlaystates(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	for _, it := range []*Item{
		{ID: "old1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "SONE-266 测试", SortName: "sone-266 测试"},
		{ID: "new1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "SONE-266 测试", SortName: "sone-266 测试"},
	} {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-new", ItemID: "new1", URL: "http://example/x", StrmPath: "/strm/new1.strm",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaystate("u1", "old1", 12345, false); err != nil {
		t.Fatal(err)
	}

	n, err := db.ReassignOrphanPlaystates()
	if err != nil || n != 1 {
		t.Fatalf("moved = %d, %v; want 1", n, err)
	}

	states, err := db.Playstates("u1", []string{"old1", "new1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := states["old1"]; ok {
		t.Fatal("old playstate should be removed")
	}
	if states["new1"].PositionTicks != 12345 {
		t.Fatalf("new1 position = %d, want 12345", states["new1"].PositionTicks)
	}
}
