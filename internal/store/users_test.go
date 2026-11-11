package store

import "testing"

func TestUserPasswordAndDisable(t *testing.T) {
	db := newTestDB(t)

	hash, err := HashPassword("old-pass")
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser("alice", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(u.PasswordHash, "old-pass") {
		t.Fatal("password hash does not verify")
	}

	token, err := db.CreateToken(u.ID, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := db.UserByToken(token); err != nil || got.ID != u.ID {
		t.Fatalf("token lookup: %v %v", got, err)
	}

	newHash, err := HashPassword("new-pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePassword(u.ID, newHash); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(got.PasswordHash, "new-pass") || VerifyPassword(got.PasswordHash, "old-pass") {
		t.Fatal("password was not updated")
	}

	// 禁用后旧 token 立即失效
	if err := db.SetUserDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByToken(token); err != ErrNotFound {
		t.Fatalf("disabled user token err = %v, want ErrNotFound", err)
	}
	got, _ = db.GetUserByID(u.ID)
	if !got.IsDisabled {
		t.Fatal("user should be disabled")
	}

	if err := db.SetUserDisabled(u.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetUserByID(u.ID)
	if got.IsDisabled {
		t.Fatal("user should be enabled")
	}
}

func TestUserAdminAndDelete(t *testing.T) {
	db := newTestDB(t)

	admin, err := db.CreateUser("admin", "x", true)
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("bob", "x", false)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := db.CountAdmins(); err != nil || n != 1 {
		t.Fatalf("admins = %d, %v", n, err)
	}

	if err := db.SetUserAdmin(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAdmins(); n != 2 {
		t.Fatalf("admins = %d, want 2", n)
	}
	if err := db.SetUserAdmin(user.ID, false); err != nil {
		t.Fatal(err)
	}

	token, err := db.CreateToken(user.ID, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUserByID(user.ID); err != ErrNotFound {
		t.Fatalf("deleted user still exists: %v", err)
	}
	if _, err := db.UserByToken(token); err != ErrNotFound {
		t.Fatal("tokens should be deleted with the user")
	}
	if _, err := db.GetUserByID(admin.ID); err != nil {
		t.Fatal("admin should still exist")
	}
}

func TestAPITokens(t *testing.T) {
	db := newTestDB(t)

	u, err := db.CreateUser("alice", "x", true)
	if err != nil {
		t.Fatal(err)
	}

	// 登录 token 不出现在 API Key 列表里
	if _, err := db.CreateToken(u.ID, "dev1"); err != nil {
		t.Fatal(err)
	}
	if keys, err := db.ListAPITokens(); err != nil || len(keys) != 0 {
		t.Fatalf("keys = %v, %v; want empty", keys, err)
	}

	key, err := db.CreateAPIToken(u.ID, "Infuse")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := db.ListAPITokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "Infuse" || keys[0].UserName != "alice" || keys[0].Token != key {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	if _, err := db.UserByToken(key); err != nil {
		t.Fatal("api key should authenticate")
	}

	// 改密码只保留当前 token
	if err := db.DeleteTokensForUserExcept(u.ID, key); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByToken(key); err != nil {
		t.Fatal("kept token should still work")
	}
	if keys, _ := db.ListAPITokens(); len(keys) != 1 {
		t.Fatal("api key should have been kept")
	}

	if err := db.DeleteToken(key); err != nil {
		t.Fatal(err)
	}
	if keys, _ := db.ListAPITokens(); len(keys) != 0 {
		t.Fatal("api key should be deleted")
	}
}
