package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWebUsersRequiresAdmin(t *testing.T) {
	f := newUserFixture(t)
	if rr := f.do(http.MethodGet, "/web/api/users", nil, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestWebUsersReportsDeviceCount(t *testing.T) {
	f := newUserFixture(t)

	if err := f.env.db.UpsertDevice(f.bob.ID, "bob-dev", "Android", "手机", "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertDevice(f.bob.ID, "bob-tv", "AndroidTV", "客厅电视", ""); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/web/api/users", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	var users []struct {
		Id                string
		Name              string
		MaxActiveSessions int
		AllowDownload     bool
		AllowPlayback     bool
		DeviceCount       int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}

	var bob *struct {
		Id                string
		Name              string
		MaxActiveSessions int
		AllowDownload     bool
		AllowPlayback     bool
		DeviceCount       int
	}
	for i := range users {
		if users[i].Id == f.bob.ID {
			bob = &users[i]
		}
	}
	if bob == nil {
		t.Fatal("bob missing from list")
	}
	if bob.DeviceCount != 2 {
		t.Fatalf("DeviceCount = %d, want 2", bob.DeviceCount)
	}
	if !bob.AllowDownload || !bob.AllowPlayback {
		t.Fatalf("new user should allow play/download by default: %+v", bob)
	}
}

// 踢下线要同时吊销 token，否则客户端还能继续用旧凭据。
func TestKickDeviceRevokesToken(t *testing.T) {
	f := newUserFixture(t)
	if err := f.env.db.UpsertDevice(f.bob.ID, "bob-dev", "Android", "手机", "203.0.113.5"); err != nil {
		t.Fatal(err)
	}

	path := "/web/api/users/" + f.bob.ID + "/devices/bob-dev"
	if rr := f.do(http.MethodDelete, path, nil, f.adminTok); rr.Code != http.StatusNoContent {
		t.Fatalf("kick status = %d: %s", rr.Code, rr.Body.String())
	}

	if rr := f.do(http.MethodGet, "/Users/Me", nil, f.bobTok); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", rr.Code)
	}

	// 设备记录也没了。
	devices, err := f.env.db.ListDevices(f.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices left: %d", len(devices))
	}
}

func TestUpdateUserPolicyLimits(t *testing.T) {
	f := newUserFixture(t)
	path := "/Users/" + f.bob.ID + "/Policy"

	rr := f.do(http.MethodPost, path, map[string]any{
		"MaxActiveSessions":        2,
		"EnableContentDownloading": false,
		"EnableMediaPlayback":      false,
	}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	updated, err := f.env.db.GetUserByID(f.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxActiveSessions != 2 {
		t.Fatalf("MaxActiveSessions = %d, want 2", updated.MaxActiveSessions)
	}
	if updated.AllowDownload || updated.AllowPlayback {
		t.Fatalf("expected both disabled: dl=%v play=%v", updated.AllowDownload, updated.AllowPlayback)
	}

	// 非法并发数要被拒。
	if rr := f.do(http.MethodPost, path, map[string]any{"MaxActiveSessions": -1}, f.adminTok); rr.Code != http.StatusBadRequest {
		t.Fatalf("negative limit status = %d, want 400", rr.Code)
	}
	if rr := f.do(http.MethodPost, path, map[string]any{"MaxActiveSessions": 500}, f.adminTok); rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized limit status = %d, want 400", rr.Code)
	}
}

// 关闭播放权限后，PlaybackInfo 必须直接拒绝。
func TestPlaybackInfoRejectedWhenPlaybackDisabled(t *testing.T) {
	f := newUserFixture(t)
	if err := f.env.db.SetUserAllowPlayback(f.bob.ID, false); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Items/does-not-exist/PlaybackInfo", nil, f.bobTok)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (playback disabled)", rr.Code)
	}
}
