package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/store"
)

func fieldsEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	env, _, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "电影", SortName: "电影", Overview: "一段简介",
		People: `[{"Name":"演员甲","Role":"主角","Type":"Actor","ImageTag":"abc"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	return env, tok
}

func getItems(t *testing.T, env *testEnv, tok, query string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/emby/Items?"+query, nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var res struct {
		Items []map[string]any `json:"Items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res.Items
}

// 没传 Fields 时保持历史行为，简介和演员表都返回。
func TestItemFieldsDefaultsToFull(t *testing.T) {
	env, tok := fieldsEnv(t)
	items := getItems(t, env, tok, "Recursive=true&IncludeItemTypes=Movie")
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0]["Overview"] != "一段简介" {
		t.Errorf("Overview = %v", items[0]["Overview"])
	}
	if people, _ := items[0]["People"].([]any); len(people) != 1 {
		t.Errorf("People = %v", items[0]["People"])
	}
}

// 传了 Fields 就只返回点名的重字段，且 People 不能是 null。
func TestItemFieldsFiltersHeavyFields(t *testing.T) {
	env, tok := fieldsEnv(t)
	items := getItems(t, env, tok, "Recursive=true&IncludeItemTypes=Movie&Fields=Name")
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	if _, ok := items[0]["Overview"]; ok {
		t.Errorf("Overview should be omitted, got %v", items[0]["Overview"])
	}
	people, ok := items[0]["People"].([]any)
	if !ok || len(people) != 0 {
		t.Errorf("People = %v, want empty array", items[0]["People"])
	}
	if items[0]["Name"] != "电影" {
		t.Errorf("Name = %v", items[0]["Name"])
	}

	items = getItems(t, env, tok, "Recursive=true&IncludeItemTypes=Movie&Fields=Overview")
	if items[0]["Overview"] != "一段简介" {
		t.Errorf("requested Overview missing: %v", items[0]["Overview"])
	}
	if people, _ := items[0]["People"].([]any); len(people) != 0 {
		t.Errorf("People should be empty, got %v", items[0]["People"])
	}
}
