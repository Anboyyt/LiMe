package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

// 首页精选：评分高的排前面，没有封面的条目直接排除。
func TestWebFeaturedOrdersByRating(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	items := []*store.Item{
		{ID: "m-low", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
			Name: "低分", SortName: "低分", ImagePath: "img/low.jpg", CommunityRating: 5},
		{ID: "m-high", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
			Name: "高分", SortName: "高分", ImagePath: "img/high.jpg", CommunityRating: 9},
		{ID: "m-noimg", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
			Name: "无封面", SortName: "无封面", CommunityRating: 10},
	}
	for _, it := range items {
		if err := f.env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	rr := f.do(http.MethodGet, "/web/api/featured?limit=8", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var res struct {
		Items []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want 2（无封面应被排除）", len(res.Items))
	}
	if res.Items[0].Id != "m-high" {
		t.Fatalf("first = %s, want m-high", res.Items[0].Id)
	}
}
