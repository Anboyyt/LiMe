package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/store"
)

// 客户端的「未看」筛选（Filters=IsUnplayed）必须真正过滤。
func TestItemsFiltersIsUnplayed(t *testing.T) {
	env, user, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("M", "/strm/m", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeMovie, Name: id, SortName: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.db.SetPlayed(user.ID, "a", true); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertPlaystate(user.ID, "b", 100, false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/Users/"+user.ID+"/Items?ParentId="+lib.ID+"&Recursive=true&Filters=IsUnplayed", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if out.TotalRecordCount != 2 || len(out.Items) != 2 {
		t.Fatalf("unplayed total=%d items=%d, want 2", out.TotalRecordCount, len(out.Items))
	}
	for _, it := range out.Items {
		if it["Name"] == "a" {
			t.Fatal("played item a should be filtered out")
		}
	}
}

// 其余常用筛选也要生效：IsPlayed / IsPlayed=false / IsResumable。
func TestItemsFiltersPlayedAndResumable(t *testing.T) {
	env, user, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("M2", "/strm/m2", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeMovie, Name: id, SortName: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.db.SetPlayed(user.ID, "a", true); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertPlaystate(user.ID, "b", 100, false); err != nil {
		t.Fatal(err)
	}

	get := func(query string) (int, []string) {
		req := httptest.NewRequest(http.MethodGet,
			"/Users/"+user.ID+"/Items?ParentId="+lib.ID+"&Recursive=true&"+query, nil)
		req.Header.Set("X-Emby-Token", tok)
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", query, rr.Code, rr.Body.String())
		}
		var out struct {
			Items            []map[string]any
			TotalRecordCount int
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(out.Items))
		for _, it := range out.Items {
			names = append(names, it["Name"].(string))
		}
		return out.TotalRecordCount, names
	}

	if total, names := get("Filters=IsPlayed"); total != 1 || names[0] != "a" {
		t.Fatalf("IsPlayed total=%d names=%v", total, names)
	}
	if total, _ := get("IsPlayed=false"); total != 2 {
		t.Fatalf("IsPlayed=false total=%d, want 2", total)
	}
	if total, names := get("Filters=IsResumable"); total != 1 || names[0] != "b" {
		t.Fatalf("IsResumable total=%d names=%v", total, names)
	}
}
