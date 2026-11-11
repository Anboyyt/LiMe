package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

// 管理类接口必须要求管理员，普通用户返回 403。
func TestAdminOnlyManagementEndpoints(t *testing.T) {
	f := newUserFixture(t)

	paths := []string{
		"/Library/Refresh",
		"/Library/Media/Updated",
		"/web/api/scan",
		"/web/api/scrape",
	}
	for _, p := range paths {
		if rr := f.do(http.MethodPost, p, nil, f.bobTok); rr.Code != http.StatusForbidden {
			t.Errorf("%s non-admin = %d, want 403", p, rr.Code)
		}
	}
}

// 普通用户拉 /Users 只能看到自己。
func TestListUsersScopedForNonAdmin(t *testing.T) {
	f := newUserFixture(t)

	rr := f.do(http.MethodGet, "/Users", nil, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var users []struct{ Name string }
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "bob" {
		t.Fatalf("users = %+v, want only bob", users)
	}

	rr = f.do(http.MethodGet, "/Users", nil, f.adminTok)
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("admin sees %d users, want 2", len(users))
	}
}

// 普通用户配置库权限后，/Views 只返回授权的库。
func TestLibraryAccessScoping(t *testing.T) {
	f := newUserFixture(t)

	lib1, err := f.env.db.CreateLibrary("库一", []string{"/tmp/lib1"}, "movies")
	if err != nil {
		t.Fatal(err)
	}
	lib2, err := f.env.db.CreateLibrary("库二", []string{"/tmp/lib2"}, "movies")
	if err != nil {
		t.Fatal(err)
	}
	for i, lib := range []*store.Library{lib1, lib2} {
		if err := f.env.db.UpsertItem(&store.Item{
			ID: "m" + strconv.Itoa(i), LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeMovie, Name: "电影" + strconv.Itoa(i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 未配置：默认两个库都可见。
	rr := f.do(http.MethodGet, "/Users/"+f.bob.ID+"/Views", nil, f.bobTok)
	var views dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 2 {
		t.Fatalf("default views = %d, want 2", len(views.Items))
	}

	// 只授权库一。
	if err := f.env.db.SetLibraryAccess(f.bob.ID, []string{lib1.ID}); err != nil {
		t.Fatal(err)
	}
	rr = f.do(http.MethodGet, "/Users/"+f.bob.ID+"/Views", nil, f.bobTok)
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 1 || views.Items[0].Id != lib1.ID {
		t.Fatalf("scoped views = %+v", views.Items)
	}

	// 统计接口也只算授权的库。
	rr = f.do(http.MethodGet, "/Items/Counts", nil, f.bobTok)
	var counts map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts["MovieCount"] != 1 {
		t.Fatalf("scoped MovieCount = %d, want 1", counts["MovieCount"])
	}
}

// 家长控制：受限用户看不到超过最高分级的条目，详情也返回 404。
func TestParentalRatingScoping(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.CreateLibrary("电影", []string{"/tmp/movies"}, "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []*store.Item{
		{ID: "g", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "G"},
		{ID: "pg13", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "PG13"},
		{ID: "r", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "R"},
		{ID: "none", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "None"},
	} {
		if err := f.env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.env.db.UpdateItemRating("g", "G")
	_ = f.env.db.UpdateItemRating("pg13", "PG-13")
	_ = f.env.db.UpdateItemRating("r", "R")

	if err := f.env.db.SetUserMaxRating(f.bob.ID, 3); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Items?ParentId="+lib.ID+"&Recursive=true", nil, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var res dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.TotalRecordCount != 3 {
		t.Fatalf("total = %d, want 3 (R hidden)", res.TotalRecordCount)
	}
	for _, it := range res.Items {
		if it.Id == "r" {
			t.Fatal("R-rated item should be hidden")
		}
	}

	if rr := f.do(http.MethodGet, "/Items/r", nil, f.bobTok); rr.Code != http.StatusNotFound {
		t.Fatalf("R detail = %d, want 404", rr.Code)
	}
	if rr := f.do(http.MethodGet, "/Items/r", nil, f.adminTok); rr.Code != http.StatusOK {
		t.Fatalf("admin R detail = %d, want 200", rr.Code)
	}
}
