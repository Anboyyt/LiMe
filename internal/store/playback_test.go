package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedPlaybackFixture 建一个用户、一个媒体库，以及 series/season/两集/一部电影。
func seedPlaybackFixture(t *testing.T, db *DB) {
	t.Helper()
	now := time.Now().Unix()
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, name, password_hash, created_at) VALUES ('u1','admin','x',?)`, []any{now}},
		{`INSERT INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','mixed',?)`, []any{now}},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	items := []*Item{
		{ID: "s1", LibraryID: "lib", ParentID: "lib", Type: TypeSeries, Name: "剧", SortName: "剧"},
		{ID: "se1", LibraryID: "lib", ParentID: "s1", Type: TypeSeason, Name: "第 1 季", SeriesID: "s1", IndexNumber: 1},
		{ID: "e1", LibraryID: "lib", ParentID: "se1", Type: TypeEpisode, Name: "第 1 集", SeriesID: "s1", SeasonID: "se1", IndexNumber: 1, ParentIndexNumber: 1},
		{ID: "e2", LibraryID: "lib", ParentID: "se1", Type: TypeEpisode, Name: "第 2 集", SeriesID: "s1", SeasonID: "se1", IndexNumber: 2, ParentIndexNumber: 1},
		{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie, Name: "电影", RunTimeTicks: 7200 * 10_000_000},
	}
	for _, it := range items {
		if err := db.UpsertItem(it); err != nil {
			t.Fatalf("upsert item %s: %v", it.ID, err)
		}
	}
}

func TestPlaybackSessionTTL(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpsertPlaybackSession(&PlaybackSession{
		ID: "fresh", UserID: "u1", ItemID: "m1", PositionTicks: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaybackSession(&PlaybackSession{
		ID: "stale", UserID: "u1", ItemID: "e1", PositionTicks: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE playback_sessions SET updated_at = ? WHERE id = 'stale'`,
		time.Now().Add(-PlaybackSessionTTL-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListPlaybackSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "fresh" {
		t.Fatalf("sessions = %+v, want only fresh", list)
	}

	// 暂停超过 TTL 只是不显示，24 小时内不删除，恢复播放还能判断暂停状态。
	if n, err := db.PurgeStalePlaybackSessions(time.Now().Add(-PlaybackSessionRetention)); err != nil || n != 0 {
		t.Fatalf("purged within retention = %d, %v; want 0", n, err)
	}
	if _, err := db.Exec(`UPDATE playback_sessions SET updated_at = ? WHERE id = 'stale'`,
		time.Now().Add(-PlaybackSessionRetention-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	n, err := db.PurgeStalePlaybackSessions(time.Now().Add(-PlaybackSessionRetention))
	if err != nil || n != 1 {
		t.Fatalf("purged = %d, %v; want 1", n, err)
	}
}

func TestResumeItemIDs(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpsertPlaystate("u1", "e1", 1200, false); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlaystate("u1", "m1", 3000, false); err != nil {
		t.Fatal(err)
	}
	// 已看完的不该出现
	if err := db.UpsertPlaystate("u1", "e2", 0, true); err != nil {
		t.Fatal(err)
	}
	// 有记录但进度为 0 的也不该出现
	if err := db.UpsertPlaystate("u1", "s1", 0, false); err != nil {
		t.Fatal(err)
	}
	// 直接改 updated_at，验证按最近播放倒序（不靠 sleep，避免测试变慢）
	now := time.Now().Unix()
	if _, err := db.Exec(
		`UPDATE playstates SET updated_at = ? WHERE user_id = 'u1' AND item_id IN ('e1','m1')`,
		now-60,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`UPDATE playstates SET updated_at = ? WHERE user_id = 'u1' AND item_id = 'm1'`,
		now,
	); err != nil {
		t.Fatal(err)
	}

	ids, total, err := db.ResumeItemIDs("u1", nil, nil, 0, 20, 0)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if total != 2 || len(ids) != 2 {
		t.Fatalf("resume total=%d ids=%v, want 2", total, ids)
	}
	if ids[0] != "m1" || ids[1] != "e1" {
		t.Fatalf("resume order = %v, want [m1 e1]", ids)
	}

	// 类型过滤
	ids, total, err = db.ResumeItemIDs("u1", []string{TypeEpisode}, nil, 0, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(ids) != 1 || ids[0] != "e1" {
		t.Fatalf("filtered resume = %v (total %d), want [e1]", ids, total)
	}
}

func TestSetPlayedAndFavorite(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpsertPlaystate("u1", "m1", 5000, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFavorite("u1", "m1", true); err != nil {
		t.Fatal(err)
	}
	states, err := db.Playstates("u1", []string{"m1"})
	if err != nil {
		t.Fatal(err)
	}
	ps := states["m1"]
	if !ps.IsFavorite {
		t.Fatal("favorite not saved")
	}
	if ps.PositionTicks != 5000 {
		t.Fatalf("favorite changed position: %d", ps.PositionTicks)
	}

	if err := db.SetPlayed("u1", "m1", true); err != nil {
		t.Fatal(err)
	}
	states, err = db.Playstates("u1", []string{"m1"})
	if err != nil {
		t.Fatal(err)
	}
	ps = states["m1"]
	if !ps.Played || ps.PositionTicks != 0 {
		t.Fatalf("played=%v position=%d, want true/0", ps.Played, ps.PositionTicks)
	}
	if !ps.IsFavorite {
		t.Fatal("SetPlayed cleared favorite")
	}

	// 取消已看：进度保持清零，收藏不受影响
	if err := db.SetPlayed("u1", "m1", false); err != nil {
		t.Fatal(err)
	}
	states, _ = db.Playstates("u1", []string{"m1"})
	if states["m1"].Played {
		t.Fatal("unset played failed")
	}
}

func TestWatchProgress(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.SetPlayed("u1", "e1", true); err != nil {
		t.Fatal(err)
	}

	progress, err := db.WatchProgress("u1", "series_id", []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}
	p := progress["s1"]
	if p.Total != 2 || p.Unplayed != 1 {
		t.Fatalf("series progress = %+v, want total 2 unplayed 1", p)
	}

	progress, err = db.WatchProgress("u1", "season_id", []string{"se1"})
	if err != nil {
		t.Fatal(err)
	}
	p = progress["se1"]
	if p.Total != 2 || p.Unplayed != 1 {
		t.Fatalf("season progress = %+v, want total 2 unplayed 1", p)
	}

	// 另一个用户不受影响
	progress, err = db.WatchProgress("nobody", "series_id", []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}
	if p = progress["s1"]; p.Unplayed != 2 {
		t.Fatalf("other user progress = %+v, want unplayed 2", p)
	}
}

func TestItemsByIDs(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	got, err := db.ItemsByIDs([]string{"e1", "m1", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got["e1"].SeriesID != "s1" || got["m1"].Type != TypeMovie {
		t.Fatalf("unexpected items: %+v", got)
	}
}
