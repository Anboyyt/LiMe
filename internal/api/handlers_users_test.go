package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/store"
)

type userFixture struct {
	env      *testEnv
	admin    *store.User
	adminTok string
	bob      *store.User
	bobTok   string
}

func newUserFixture(t *testing.T) *userFixture {
	t.Helper()
	env := newTestEnv(t)

	adminHash, _ := store.HashPassword("admin-pw")
	admin, err := env.db.CreateUser("admin", adminHash, true)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := env.db.CreateToken(admin.ID, "admin-dev")
	if err != nil {
		t.Fatal(err)
	}

	bobHash, _ := store.HashPassword("bob-pw")
	bob, err := env.db.CreateUser("bob", bobHash, false)
	if err != nil {
		t.Fatal(err)
	}
	bobTok, err := env.db.CreateToken(bob.ID, "bob-dev")
	if err != nil {
		t.Fatal(err)
	}

	return &userFixture{env: env, admin: admin, adminTok: adminTok, bob: bob, bobTok: bobTok}
}

func (f *userFixture) do(method, path string, body any, token string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(raw))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("X-Emby-Token", token)
	}
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	return rr
}

func TestCreateUserPermissions(t *testing.T) {
	f := newUserFixture(t)

	if rr := f.do(http.MethodPost, "/Users/New", map[string]string{"Name": "alice", "Password": "pw"}, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin create = %d, want 403", rr.Code)
	}

	rr := f.do(http.MethodPost, "/Users/New", map[string]string{"Name": "alice", "Password": "pw"}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin create = %d: %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Id   string
		Name string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "alice" || created.Id == "" {
		t.Fatalf("created = %+v", created)
	}

	// 重名
	if rr := f.do(http.MethodPost, "/Users/New", map[string]string{"Name": "alice"}, f.adminTok); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", rr.Code)
	}
}

func TestRenameUser(t *testing.T) {
	f := newUserFixture(t)

	// 本人改名
	rr := f.do(http.MethodPost, "/Users/"+f.bob.ID, map[string]string{"Name": "bobby"}, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("rename self = %d: %s", rr.Code, rr.Body.String())
	}
	if u, err := f.env.db.GetUserByID(f.bob.ID); err != nil || u.Name != "bobby" {
		t.Fatalf("user = %+v, %v", u, err)
	}

	// 普通用户不能改别人
	if rr := f.do(http.MethodPost, "/Users/"+f.admin.ID, map[string]string{"Name": "hacker"}, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("rename other = %d, want 403", rr.Code)
	}

	// 重名冲突
	if rr := f.do(http.MethodPost, "/Users/"+f.admin.ID, map[string]string{"Name": "bobby"}, f.adminTok); rr.Code != http.StatusConflict {
		t.Fatalf("rename to existing = %d, want 409", rr.Code)
	}

	// 管理员可以改别人
	rr = f.do(http.MethodPost, "/Users/"+f.bob.ID, map[string]string{"Name": "robert"}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin rename = %d", rr.Code)
	}
	if u, _ := f.env.db.GetUserByID(f.bob.ID); u.Name != "robert" {
		t.Fatalf("name = %q, want robert", u.Name)
	}
}

func TestUpdatePasswordAndRevoke(t *testing.T) {
	f := newUserFixture(t)

	otherTok, err := f.env.db.CreateToken(f.bob.ID, "bob-other")
	if err != nil {
		t.Fatal(err)
	}

	// 旧密码不对
	rr := f.do(http.MethodPost, "/Users/"+f.bob.ID+"/Password",
		map[string]string{"CurrentPw": "wrong", "NewPw": "new-pw"}, f.bobTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong current pw = %d, want 400", rr.Code)
	}

	// 正确修改
	rr = f.do(http.MethodPost, "/Users/"+f.bob.ID+"/Password",
		map[string]string{"CurrentPw": "bob-pw", "NewPw": "new-pw"}, f.bobTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("change pw = %d: %s", rr.Code, rr.Body.String())
	}

	// 当前 token 保留，其他设备 token 失效
	if _, err := f.env.db.UserByToken(f.bobTok); err != nil {
		t.Fatal("current token should survive password change")
	}
	if _, err := f.env.db.UserByToken(otherTok); err != store.ErrNotFound {
		t.Fatal("other tokens should be revoked")
	}

	// 新密码能登录
	rr = f.do(http.MethodPost, "/Users/AuthenticateByName",
		map[string]string{"Username": "bob", "Pw": "new-pw"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("login with new pw = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDisableUser(t *testing.T) {
	f := newUserFixture(t)

	disabled := false
	rr := f.do(http.MethodPost, "/Users/"+f.bob.ID+"/Policy",
		map[string]any{"IsDisabled": true, "IsAdministrator": disabled}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable = %d: %s", rr.Code, rr.Body.String())
	}

	// 被禁用后 token 立即失效
	if rr := f.do(http.MethodGet, "/Users/Me", nil, f.bobTok); rr.Code != http.StatusUnauthorized {
		t.Fatalf("disabled token = %d, want 401", rr.Code)
	}
	// 也不能再登录
	rr = f.do(http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": "bob", "Pw": "bob-pw"}, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login = %d, want 401", rr.Code)
	}

	// 管理员不能把自己降权/禁用成最后一个管理员
	rr = f.do(http.MethodPost, "/Users/"+f.admin.ID+"/Policy", map[string]any{"IsAdministrator": false}, f.adminTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("demote last admin = %d, want 400", rr.Code)
	}
}

func TestDeleteUser(t *testing.T) {
	f := newUserFixture(t)

	if rr := f.do(http.MethodDelete, "/Users/"+f.bob.ID, nil, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete = %d, want 403", rr.Code)
	}
	if rr := f.do(http.MethodDelete, "/Users/"+f.admin.ID, nil, f.adminTok); rr.Code != http.StatusBadRequest {
		t.Fatalf("delete self = %d, want 400", rr.Code)
	}
	if rr := f.do(http.MethodDelete, "/Users/"+f.bob.ID, nil, f.adminTok); rr.Code != http.StatusNoContent {
		t.Fatalf("delete bob = %d", rr.Code)
	}
	if _, err := f.env.db.GetUserByID(f.bob.ID); err != store.ErrNotFound {
		t.Fatalf("bob still exists: %v", err)
	}
}

func TestAPIKeys(t *testing.T) {
	f := newUserFixture(t)

	if rr := f.do(http.MethodGet, "/Auth/Keys", nil, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin keys = %d, want 403", rr.Code)
	}

	if rr := f.do(http.MethodPost, "/Auth/Keys?App=Infuse", nil, f.adminTok); rr.Code != http.StatusNoContent {
		t.Fatalf("create key = %d: %s", rr.Code, rr.Body.String())
	}

	rr := f.do(http.MethodGet, "/Auth/Keys", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("list keys = %d", rr.Code)
	}
	var keys []apiKeyDto
	if err := json.Unmarshal(rr.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].AppName != "Infuse" || keys[0].AccessToken == "" {
		t.Fatalf("keys = %+v", keys)
	}

	// 这个 key 能直接当 token 用
	if rr := f.do(http.MethodGet, "/Users/Me", nil, keys[0].AccessToken); rr.Code != http.StatusOK {
		t.Fatalf("api key auth = %d", rr.Code)
	}

	if rr := f.do(http.MethodDelete, "/Auth/Keys/"+keys[0].AccessToken, nil, f.adminTok); rr.Code != http.StatusNoContent {
		t.Fatalf("delete key = %d", rr.Code)
	}
	if rr := f.do(http.MethodGet, "/Auth/Keys", nil, f.adminTok); len(rr.Body.String()) != 3 { // "[]\n"
		t.Fatalf("keys after delete = %q", rr.Body.String())
	}
}

func TestLogout(t *testing.T) {
	f := newUserFixture(t)

	if rr := f.do(http.MethodPost, "/Sessions/Logout", nil, f.bobTok); rr.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rr.Code)
	}
	if rr := f.do(http.MethodGet, "/Users/Me", nil, f.bobTok); rr.Code != http.StatusUnauthorized {
		t.Fatalf("token after logout = %d, want 401", rr.Code)
	}
}
