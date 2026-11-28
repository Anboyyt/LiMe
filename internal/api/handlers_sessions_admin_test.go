package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

func TestLoginLogRecordsSuccessAndFailure(t *testing.T) {
	f := newUserFixture(t)

	// 成功
	rr := f.do(http.MethodPost, "/Users/AuthenticateByName",
		map[string]any{"Username": "bob", "Pw": "bob-pw"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d", rr.Code)
	}
	// 失败
	rr = f.do(http.MethodPost, "/Users/AuthenticateByName",
		map[string]any{"Username": "bob", "Pw": "wrong"}, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d", rr.Code)
	}

	rr = f.do(http.MethodGet, "/web/api/login-log?userId="+f.bob.ID, nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("log status = %d: %s", rr.Code, rr.Body.String())
	}
	var logs []struct {
		UserName string
		Success  bool
		Reason   string
		IP       string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %d, want 2", len(logs))
	}
	// 最新的在前：失败那条
	if logs[0].Success {
		t.Fatalf("newest log should be the failed one: %+v", logs[0])
	}
	if !logs[1].Success {
		t.Fatalf("older log should be the success: %+v", logs[1])
	}
}

func TestWebSessionDelete(t *testing.T) {
	f := newUserFixture(t)
	if err := f.env.db.UpsertPlaybackSession(&store.PlaybackSession{
		ID: "sess-1", UserID: f.bob.ID, DeviceID: "dev", ItemID: "item-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertPlaybackSession(&store.PlaybackSession{
		ID: "sess-2", UserID: f.bob.ID, DeviceID: "dev", ItemID: "item-2",
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodDelete, "/web/api/sessions/sess-1", nil, f.adminTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	sessions, err := f.env.db.ListPlaybackSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "sess-2" {
		t.Fatalf("sessions left = %+v", sessions)
	}

	// 普通用户不能停会话
	if rr := f.do(http.MethodDelete, "/web/api/sessions/sess-2", nil, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rr.Code)
	}
}

func TestCreateUserPasswordPolicy(t *testing.T) {
	f := newUserFixture(t)

	cases := []struct {
		name string
		pw   string
		want int
	}{
		{"u1", "abc", http.StatusBadRequest},       // 太短
		{"u2", "u2", http.StatusBadRequest},        // 和用户名相同
		{"u3", "abcd", http.StatusOK},              // 合法
		{"u4", "", http.StatusOK},                  // 允许空密码（旧行为）
	}
	for _, c := range cases {
		rr := f.do(http.MethodPost, "/Users/New",
			map[string]any{"Name": c.name, "Password": c.pw}, f.adminTok)
		if rr.Code != c.want {
			t.Errorf("%s/%q: status = %d, want %d (%s)", c.name, c.pw, rr.Code, c.want, rr.Body.String())
		}
	}
}

func TestUpdateUserPolicyPlayWindow(t *testing.T) {
	f := newUserFixture(t)
	path := "/Users/" + f.bob.ID + "/Policy"

	// 非法时间
	rr := f.do(http.MethodPost, path, map[string]any{
		"PlayWindow": map[string]any{"Start": "25:00", "End": "26:00"},
	}, f.adminTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid window status = %d, want 400", rr.Code)
	}

	// 合法
	rr = f.do(http.MethodPost, path, map[string]any{
		"PlayWindow": map[string]any{"Start": "18:00", "End": "23:00", "Days": []int{1, 2, 3}},
	}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	u, err := f.env.db.GetUserByID(f.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := u.PlayWindow(); w.Start != "18:00" || len(w.Days) != 3 {
		t.Fatalf("window = %+v", w)
	}

	// 清空
	rr = f.do(http.MethodPost, path, map[string]any{
		"PlayWindow": map[string]any{"Start": "", "End": ""},
	}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rr.Code)
	}
	u, _ = f.env.db.GetUserByID(f.bob.ID)
	if !u.PlayWindow().IsZero() {
		t.Fatalf("window should be cleared: %+v", u.PlayWindowRaw)
	}

}
