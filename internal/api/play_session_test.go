package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

// 同一设备短时间内重进同一个视频必须复用 PlaySessionId，否则 PanSave
// 每次都会分配新副本（实测连续 4 次，每次 1 秒左右）。
func TestPlaybackSessionReusedPerDeviceAndItem(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.Item{
		ID: "ps1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "测试剧", SortName: "测试剧",
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "pssrc", ItemID: it.ID, StrmPath: "/strm/ps1.strm",
		URL: "http://example", Container: "mp4", FileName: "ps1.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	play := func(device string) string {
		req := httptest.NewRequest(http.MethodPost, "/Items/"+it.ID+"/PlaybackInfo", strings.NewReader("{}"))
		req.Header.Set("X-Emby-Token", f.adminTok)
		req.Header.Set("Content-Type", "application/json")
		if device != "" {
			req.Header.Set("X-Emby-Device-Id", device)
		}
		rr := httptest.NewRecorder()
		f.env.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		var info dto.PlaybackInfoResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info.PlaySessionId
	}

	a1 := play("dev-a")
	a2 := play("dev-a")
	if a1 == "" || a1 != a2 {
		t.Fatalf("same device session = %q / %q, want reused", a1, a2)
	}

	b1 := play("dev-b")
	if b1 == a1 {
		t.Fatalf("different device should get its own session, got %q", b1)
	}
}
