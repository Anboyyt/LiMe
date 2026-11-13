package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"lime/internal/store"
)

// seedPersonsEnv 建两个演员、三部片子，用于验证 PersonIds 反查。
func seedPersonsEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	items := []*store.Item{
		{ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "有汤姆", SortName: "有汤姆",
			People: `[{"Name":"Tom Hanks","Role":"主角","Type":"Actor"}]`},
		{ID: "m2", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "无汤姆", SortName: "无汤姆",
			People: `[{"Name":"Someone Else","Role":"主角","Type":"Actor"}]`},
		{ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries, Name: "汤姆的剧", SortName: "汤姆的剧",
			People: `[{"Name":"Tom Hanks","Role":"主角","Type":"Actor"}]`},
	}
	for _, it := range items {
		if err := env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.db.UpsertPersons([]store.PersonImage{
		{ID: store.PersonID("Tom Hanks"), Name: "Tom Hanks", ImageURL: "https://example.com/tom.jpg"},
		{ID: store.PersonID("Someone Else"), Name: "Someone Else"},
	}); err != nil {
		t.Fatal(err)
	}
	return env, tok
}

func getJSON(t *testing.T, env *testEnv, tok, path string, out any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body = %s", path, rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatalf("%s json: %v body=%s", path, err, rr.Body.String())
	}
}

// 客户端点演员会请求 /Items?PersonIds=<id>，必须只返回该演员的片。
func TestItemsFilterByPersonIds(t *testing.T) {
	env, tok := seedPersonsEnv(t)

	var res struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	getJSON(t, env, tok, "/Items?Recursive=true&IncludeItemTypes=Movie,Series&PersonIds="+
		store.PersonID("Tom Hanks"), &res)
	if res.TotalRecordCount != 2 || len(res.Items) != 2 {
		t.Fatalf("total=%d items=%d, want 2", res.TotalRecordCount, len(res.Items))
	}
	for _, it := range res.Items {
		if it["Id"] == "m2" {
			t.Fatal("unrelated movie should be filtered out")
		}
	}
}

// 未知演员 ID 不能退化成整库。
func TestItemsFilterByUnknownPersonId(t *testing.T) {
	env, tok := seedPersonsEnv(t)

	var res struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	getJSON(t, env, tok, "/Items?Recursive=true&PersonIds=00000000000000000000000000000000", &res)
	if res.TotalRecordCount != 0 || len(res.Items) != 0 {
		t.Fatalf("unknown person total=%d items=%d, want 0", res.TotalRecordCount, len(res.Items))
	}
}

// 演员详情页：/Items/{personId}、/Persons/{id}、/Persons/{name} 都要能用。
func TestPersonDetailRoutes(t *testing.T) {
	env, tok := seedPersonsEnv(t)
	pid := store.PersonID("Tom Hanks")

	var item map[string]any
	getJSON(t, env, tok, "/Items/"+pid, &item)
	if item["Type"] != "Person" || item["Name"] != "Tom Hanks" {
		t.Fatalf("item person = %v", item)
	}

	var person map[string]any
	getJSON(t, env, tok, "/Persons/"+pid, &person)
	if person["Name"] != "Tom Hanks" || person["Id"] != pid {
		t.Fatalf("person by id = %v", person)
	}

	getJSON(t, env, tok, "/Persons/"+url.PathEscape("Tom Hanks"), &person)
	if person["Name"] != "Tom Hanks" {
		t.Fatalf("person by name = %v", person)
	}
}

// 演员列表接口不再固定返回空数组。
func TestPersonsList(t *testing.T) {
	env, tok := seedPersonsEnv(t)

	var res struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	getJSON(t, env, tok, "/Persons?SearchTerm=Tom", &res)
	if res.TotalRecordCount != 1 || len(res.Items) != 1 {
		t.Fatalf("persons total=%d items=%d, want 1", res.TotalRecordCount, len(res.Items))
	}
	got := res.Items[0]
	if got["Name"] != "Tom Hanks" || got["Type"] != "Person" {
		t.Fatalf("person = %v", got)
	}
	if tags, _ := got["ImageTags"].(map[string]any); tags["Primary"] == nil {
		t.Fatalf("ImageTags = %v", got["ImageTags"])
	}
}
