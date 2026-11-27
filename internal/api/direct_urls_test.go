package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

// 不读 DirectStreamUrl 的客户端（爆米花等）不该触发列表页批量解析，
// 否则每翻一页都要白等一次 PanSave 探测。
func TestDirectURLsSkippedForUnknownClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://cdn.example/movie.mkv")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.PlaybackDirectURL = true
		c.ResolveRedirect = true
	})
	lib, err := f.env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "e1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeEpisode,
		Name: "第 1 集", SortName: "0001",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "s1", ItemID: "e1", URL: upstream.URL, Container: "mkv",
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.doUA(http.MethodGet,
		"/Items?ParentId="+lib.ID+"&Recursive=true&IncludeItemTypes=Episode",
		nil, "Emby/3.4.0 (Android)", f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "cdn.example") {
		t.Fatal("unknown client should not get a resolved direct url")
	}
}
