package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

func TestGenresFiltersAndItemMetadata(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "电影", SortName: "电影", TmdbID: "1", ProductionYear: 2024,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpdateItemMetadata("m1", store.MetadataUpdate{
		Genres: `["动画","奇幻"]`,
		People: `[{"Name":"张三","Role":"主角","Type":"Actor"}]`,
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Items/Filters", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("filters = %d: %s", rr.Code, rr.Body.String())
	}
	var filters struct {
		Genres []string
		Years  []int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &filters); err != nil {
		t.Fatal(err)
	}
	if len(filters.Genres) != 2 || filters.Genres[0] != "动画" || len(filters.Years) != 1 || filters.Years[0] != 2024 {
		t.Fatalf("filters = %+v", filters)
	}

	rr = f.do(http.MethodGet, "/Genres", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("genres = %d: %s", rr.Code, rr.Body.String())
	}
	var genres dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &genres); err != nil {
		t.Fatal(err)
	}
	if genres.TotalRecordCount != 2 || len(genres.Items) != 2 || genres.Items[0].Type != "Genre" {
		t.Fatalf("genres = %+v", genres)
	}

	rr = f.do(http.MethodGet, "/Items/m1", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("item = %d: %s", rr.Code, rr.Body.String())
	}
	var item dto.BaseItemDto
	if err := json.Unmarshal(rr.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Genres) != 2 || item.Genres[0] != "动画" ||
		len(item.People) != 1 || item.People[0].Name != "张三" || item.People[0].Id == "" {
		t.Fatalf("item metadata = %+v", item)
	}
}
