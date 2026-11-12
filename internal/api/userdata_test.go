package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/store"
)

func seedMovie(t *testing.T, env *testEnv, id string) {
	t.Helper()
	lib, err := env.db.UpsertLibrary("M", "/strm/m", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: id, LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: id, SortName: id,
	}); err != nil {
		t.Fatal(err)
	}
}

// 管理员替别的用户标记已看：必须写到路径里的 userId，而不是管理员自己。
func TestPlayedItemsUsesPathUser(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("admin", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	victim, err := env.db.CreateUser("victim", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "dev")
	if err != nil {
		t.Fatal(err)
	}
	seedMovie(t, env, "mv1")

	req := httptest.NewRequest(http.MethodPost, "/Users/"+victim.ID+"/PlayedItems/mv1", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	adminStates, _ := env.db.Playstates(admin.ID, []string{"mv1"})
	victimStates, _ := env.db.Playstates(victim.ID, []string{"mv1"})
	if adminStates["mv1"].Played {
		t.Fatal("admin should not be marked played")
	}
	if !victimStates["mv1"].Played {
		t.Fatal("victim should be marked played")
	}
}

// 普通用户不能替别人改状态。
func TestPlayedItemsRejectsOtherUser(t *testing.T) {
	env, caller, tok := newAuthedEnv(t)
	hash, _ := store.HashPassword("pw")
	other, err := env.db.CreateUser("other", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	seedMovie(t, env, "mv2")

	req := httptest.NewRequest(http.MethodPost, "/Users/"+other.ID+"/PlayedItems/mv2", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}

	callerStates, _ := env.db.Playstates(caller.ID, []string{"mv2"})
	otherStates, _ := env.db.Playstates(other.ID, []string{"mv2"})
	if callerStates["mv2"].Played || otherStates["mv2"].Played {
		t.Fatal("no one should be marked played")
	}
}
