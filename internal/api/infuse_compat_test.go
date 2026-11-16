package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/store"
)

// 模拟 Infuse 8.x 添加 Jellyfin 服务器时的请求序列。
// 参考其实际请求：AuthenticateByName -> UserViews -> GroupingOptions
// -> UserItems/Resume -> Items/Latest -> Items/{id}。
func TestInfuseCompatibilityFlow(t *testing.T) {
	env, user, _ := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "沙丘2", SortName: "沙丘2", ProductionYear: 2024,
		PremiereDate: "2024-03-01",
	}); err != nil {
		t.Fatal(err)
	}

	// Infuse 兼容：登录参数放 query，没有 JSON body。
	login := httptest.NewRequest(http.MethodPost,
		"/Users/AuthenticateByName?Username="+user.Name+"&Pw=pw", nil)
	login.Header.Set("X-Emby-Authorization",
		`MediaBrowser Client="Infuse-Direct", Device="Apple TV", DeviceId="infuse-test", Version="8.5"`)
	lr := httptest.NewRecorder()
	env.handler.ServeHTTP(lr, login)
	if lr.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", lr.Code, lr.Body.String())
	}
	var auth struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &auth); err != nil || auth.AccessToken == "" {
		t.Fatalf("login token: %v, body = %s", err, lr.Body.String())
	}

	// 部分客户端把登录参数放在 form body 里。
	formLogin := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName",
		strings.NewReader("Username="+user.Name+"&Pw=pw"))
	formLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formLogin.Header.Set("X-Emby-Authorization",
		`MediaBrowser Client="Infuse-Library", Device="Apple TV", DeviceId="infuse-form", Version="8.5"`)
	frLogin := httptest.NewRecorder()
	env.handler.ServeHTTP(frLogin, formLogin)
	if frLogin.Code != http.StatusOK {
		t.Fatalf("form login = %d: %s", frLogin.Code, frLogin.Body.String())
	}

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Emby-Token", auth.AccessToken)
		req.Header.Set("User-Agent", "Infuse-Direct/8.5")
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		return rr
	}

	for _, path := range []string{
		"/UserViews?userId=" + user.ID + "&includeExternalContent=false",
		"/UserViews/GroupingOptions?userId=" + user.ID,
		"/Users/" + user.ID + "/Views",
		"/Users/" + user.ID + "/GroupingOptions",
		"/Packages",
		"/Repositories",
		"/DisplayPreferences/usersettings?userId=" + user.ID + "&client=emby",
		"/UserItems/Resume?userId=" + user.ID + "&limit=12&mediaTypes=Video",
		"/Items/Latest?userId=" + user.ID + "&limit=5&includeItemTypes=Movie",
		"/Items/m1?userId=" + user.ID + "&fields=DateCreated,Genres,MediaSources,Overview,People",
		"/UserItems/m1/UserData?userId=" + user.ID,
	} {
		if rr := do(path); rr.Code != http.StatusOK {
			t.Errorf("%s = %d: %s", path, rr.Code, rr.Body.String())
		}
	}

	// Infuse 解析 Views 时如果 DateCreated 缺失会停止后续请求。
	views := do("/UserViews?userId=" + user.ID)
	var viewResult struct {
		Items []struct {
			Id          string `json:"Id"`
			DateCreated string `json:"DateCreated"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(views.Body.Bytes(), &viewResult); err != nil {
		t.Fatal(err)
	}
	if len(viewResult.Items) == 0 || viewResult.Items[0].DateCreated == "" {
		t.Fatalf("UserViews DateCreated missing: %s", views.Body.String())
	}

	// Infuse 使用 camelCase 查询参数；必须被识别，不能退化成无筛选/无分页查询。
	itemsPath := "/Users/" + user.ID + "/Items/?parentId=" + viewResult.Items[0].Id +
		"&recursive=true&includeItemTypes=Movie&limit=3&fields=DateCreated,Overview" +
		"&enableTotalRecordCount=false"
	itemsResp := do(itemsPath)
	if itemsResp.Code != http.StatusOK {
		t.Fatalf("camelCase items = %d: %s", itemsResp.Code, itemsResp.Body.String())
	}
	var itemsResult struct {
		Items []struct {
			Type         string `json:"Type"`
			PremiereDate string `json:"PremiereDate"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(itemsResp.Body.Bytes(), &itemsResult); err != nil {
		t.Fatal(err)
	}
	// Infuse 用严格 DateTime 解码，date-only 会让整页失败。
	for _, it := range itemsResult.Items {
		if strings.Count(it.PremiereDate, "T") != 1 || !strings.HasSuffix(it.PremiereDate, "Z") {
			t.Fatalf("PremiereDate is not full ISO 8601: %q", it.PremiereDate)
		}
	}
	if len(itemsResult.Items) > 3 {
		t.Fatalf("limit ignored: %d items", len(itemsResult.Items))
	}
	for _, it := range itemsResult.Items {
		if it.Type != "Movie" {
			t.Fatalf("includeItemTypes ignored: got %s", it.Type)
		}
	}

	// Infuse 打开库时会请求 /Users/{id}/Items/{libraryId}。
	libResp := do("/Users/" + user.ID + "/Items/" + viewResult.Items[0].Id)
	if libResp.Code != http.StatusOK {
		t.Fatalf("library item = %d: %s", libResp.Code, libResp.Body.String())
	}
	var libItem struct {
		Type           string `json:"Type"`
		CollectionType string `json:"CollectionType"`
	}
	if err := json.Unmarshal(libResp.Body.Bytes(), &libItem); err != nil {
		t.Fatal(err)
	}
	if libItem.Type != "CollectionFolder" || libItem.CollectionType == "" {
		t.Fatalf("library item payload = %+v", libItem)
	}

	// DisplayPreferences 的 SortBy 必须是字符串，数组会让 Infuse 解码失败。
	dp := do("/DisplayPreferences/usersettings?userId=" + user.ID + "&client=emby")
	var prefs struct {
		SortBy          string `json:"SortBy"`
		SortOrder       string `json:"SortOrder"`
		ScrollDirection string `json:"ScrollDirection"`
	}
	if err := json.Unmarshal(dp.Body.Bytes(), &prefs); err != nil {
		t.Fatalf("DisplayPreferences decode: %v, body = %s", err, dp.Body.String())
	}
	if prefs.SortBy == "" || prefs.SortOrder == "" || prefs.ScrollDirection == "" {
		t.Fatalf("DisplayPreferences incomplete: %s", dp.Body.String())
	}

	// Infuse 新接口用不带 userId 路径的收藏/已看入口。
	fav := httptest.NewRequest(http.MethodPost, "/UserFavoriteItems/m1", nil)
	fav.Header.Set("X-Emby-Token", auth.AccessToken)
	fr := httptest.NewRecorder()
	env.handler.ServeHTTP(fr, fav)
	if fr.Code != http.StatusOK || !strings.Contains(fr.Body.String(), `"IsFavorite":true`) {
		t.Fatalf("favorite = %d: %s", fr.Code, fr.Body.String())
	}
}
