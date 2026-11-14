package api

import (
	"net/http"
	"testing"

	"lime/internal/store"
)

func playbackItem(t *testing.T, f *userFixture, id string) *store.Item {
	t.Helper()
	lib, err := f.env.db.UpsertLibrary("剧集", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.Item{
		ID: id, LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeEpisode, Name: "第 1 集", SortName: "0001",
		RunTimeTicks: 1000 * 10_000_000, // 1000 秒
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	return it
}

// 进度心跳到 90% 就应标记已播，不能只等 Stopped。
func TestProgressAt90PercentMarksPlayed(t *testing.T) {
	f := newUserFixture(t)
	it := playbackItem(t, f, "e90")
	session := "sess-90"

	f.do(http.MethodPost, "/Sessions/Playing", map[string]any{
		"ItemId": it.ID, "PlaySessionId": session, "PositionTicks": 0,
	}, f.bobTok)

	f.do(http.MethodPost, "/Sessions/Playing/Progress", map[string]any{
		"ItemId": it.ID, "PlaySessionId": session,
		"PositionTicks": it.RunTimeTicks * 89 / 100,
	}, f.bobTok)
	states, err := f.env.db.Playstates(f.bob.ID, []string{it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if states[it.ID].Played {
		t.Fatal("89% should not be marked played")
	}

	f.do(http.MethodPost, "/Sessions/Playing/Progress", map[string]any{
		"ItemId": it.ID, "PlaySessionId": session,
		"PositionTicks": it.RunTimeTicks * 90 / 100,
	}, f.bobTok)
	states, err = f.env.db.Playstates(f.bob.ID, []string{it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !states[it.ID].Played {
		t.Fatal("90% should be marked played")
	}
}

// 客户端 Stopped 里 PositionTicks=0 时，用最后一次进度判断是否已播。
func TestStopWithZeroPositionUsesLastProgress(t *testing.T) {
	f := newUserFixture(t)
	it := playbackItem(t, f, "e96")
	session := "sess-96"

	if err := f.env.db.UpsertPlaystate(f.bob.ID, it.ID, it.RunTimeTicks*96/100, false); err != nil {
		t.Fatal(err)
	}
	f.do(http.MethodPost, "/Sessions/Playing/Stopped", map[string]any{
		"ItemId": it.ID, "PlaySessionId": session, "PositionTicks": 0,
	}, f.bobTok)

	states, err := f.env.db.Playstates(f.bob.ID, []string{it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !states[it.ID].Played {
		t.Fatal("stop at 0 should fall back to last position and mark played")
	}
}

// 客户端缓存了已删除条目的 ID 时，进度上报不应返回 404，
// 否则部分播放器会整页报「连接失败」。
func TestSessionPlayingUnknownItemReturnsNoContent(t *testing.T) {
	f := newUserFixture(t)

	rr := f.do(http.MethodPost, "/emby/Sessions/Playing", map[string]any{
		"ItemId":        "deleted-item-id",
		"PositionTicks": 0,
	}, f.bobTok)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s, want 204", rr.Code, rr.Body.String())
	}
}
