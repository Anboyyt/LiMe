package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

type viewsResult struct {
	Items []struct {
		Name string
		Type string
	}
	TotalRecordCount int
}

func setupViewsFixture(t *testing.T) (*userFixture, *store.Library) {
	t.Helper()
	f := newUserFixture(t)

	movieLib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.db.UpsertLibrary("剧集", "/strm/剧集", "tvshows"); err != nil {
		t.Fatal(err)
	}

	// 库里的媒体条目不应出现在无 ParentId 的首页查询里。
	if err := f.env.db.UpsertItem(&store.Item{
		ID:        "m1",
		LibraryID: movieLib.ID,
		ParentID:  movieLib.ID,
		Type:      store.TypeMovie,
		Name:      "A Movie",
		SortName:  "a movie",
	}); err != nil {
		t.Fatal(err)
	}
	return f, movieLib
}

// 无 ParentId 的非递归 /Items 对应 Jellyfin 的用户根目录，应返回媒体库视图。
func TestItemsWithoutParentReturnsLibraryViews(t *testing.T) {
	f, _ := setupViewsFixture(t)

	rr := f.do(http.MethodGet, "/emby/Users/"+f.bob.ID+"/Items", nil, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var res viewsResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.TotalRecordCount != 2 || len(res.Items) != 2 {
		t.Fatalf("got total=%d items=%d, want 2/2", res.TotalRecordCount, len(res.Items))
	}
	for _, it := range res.Items {
		if it.Type != "CollectionFolder" {
			t.Errorf("item %q type = %q, want CollectionFolder", it.Name, it.Type)
		}
	}
}

// 指定 IncludeItemTypes 时按类型过滤视图；Movie 不在根目录下，结果应为空。
func TestItemsWithoutParentTypeFilter(t *testing.T) {
	f, _ := setupViewsFixture(t)

	rr := f.do(http.MethodGet, "/emby/Users/"+f.bob.ID+"/Items?IncludeItemTypes=Movie", nil, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var res viewsResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.TotalRecordCount != 0 || len(res.Items) != 0 {
		t.Fatalf("got total=%d items=%d, want 0/0", res.TotalRecordCount, len(res.Items))
	}
}

// 客户端不带 Limit 时，单次返回条数必须被 LIME_MAX_ITEMS 截断；
// TotalRecordCount 保持全量，客户端仍可翻页。
func TestItemsLimitClamped(t *testing.T) {
	env := newTestEnvWithConfig(t, func(c *config.Config) { c.MaxItems = 2 })

	hash, _ := store.HashPassword("pw")
	user, err := env.db.CreateUser("viewer", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(user.ID, "dev")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := env.db.UpsertLibrary("movies", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := env.db.UpsertItem(&store.Item{
			ID:        fmt.Sprintf("m%d", i),
			LibraryID: lib.ID,
			ParentID:  lib.ID,
			Type:      store.TypeMovie,
			Name:      fmt.Sprintf("Movie %d", i),
			SortName:  fmt.Sprintf("movie %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet,
		"/emby/Users/"+user.ID+"/Items?ParentId="+lib.ID, nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var res viewsResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.TotalRecordCount != 5 || len(res.Items) != 2 {
		t.Fatalf("got total=%d items=%d, want 5/2", res.TotalRecordCount, len(res.Items))
	}
}
