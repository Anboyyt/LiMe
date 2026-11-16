package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

type lookupResult struct {
	Items []struct {
		Id   string
		Name string
	}
	TotalRecordCount int
}

func seedLookupMovies(t *testing.T, f *userFixture) *store.Library {
	t.Helper()
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ id, name string }{
		{"m1", "A Movie"},
		{"m2", "B Movie"},
		{"m3", "C Movie"},
	} {
		if err := f.env.db.UpsertItem(&store.Item{
			ID:        m.id,
			LibraryID: lib.ID,
			ParentID:  lib.ID,
			Type:      store.TypeMovie,
			Name:      m.name,
			SortName:  m.name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return lib
}

// ?Ids= 必须按 ID 返回，不能因为没带 ParentId 就退化成库视图。
func TestItemsIdsLookup(t *testing.T) {
	f := newUserFixture(t)
	seedLookupMovies(t, f)

	rr := f.do(http.MethodGet, "/Items?Ids=m1,m3&Recursive=true", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var got lookupResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 items, got %d (%+v)", len(got.Items), got.Items)
	}
	for _, it := range got.Items {
		if it.Id != "m1" && it.Id != "m3" {
			t.Fatalf("unexpected item %q", it.Id)
		}
	}
}

func TestFavoriteItemsList(t *testing.T) {
	f := newUserFixture(t)
	seedLookupMovies(t, f)
	if err := f.env.db.SetFavorite(f.admin.ID, "m2", true); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Users/"+f.admin.ID+"/FavoriteItems", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var got lookupResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Id != "m2" {
		t.Fatalf("favorites = %+v", got.Items)
	}

	// 普通用户不能读别人的收藏。
	rr = f.do(http.MethodGet, "/Users/"+f.admin.ID+"/FavoriteItems", nil, f.bobTok)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob reading admin favorites: status = %d", rr.Code)
	}
}
