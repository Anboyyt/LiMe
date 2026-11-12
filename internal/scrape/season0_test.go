package scrape

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

type seasonZeroFixture struct {
	scraper *Scraper
	db      *store.DB
	mu      *sync.Mutex
	paths   *[]string
}

// 第 0 季必须按 TMDB 的 season/0 查询；回退成 season/1 会拿错数据，
// 且 EpisodeByNumber(series,1,...) 永远匹配不到第 0 季的单集。
func newSeasonZeroFixture(t *testing.T) *seasonZeroFixture {
	t.Helper()

	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/tv/123/season/0":
			_, _ = io.WriteString(w, `{"season_number":0,"name":"特典","overview":"ova 简介","episodes":[{"episode_number":1,"name":"特典一","overview":"特典简介","air_date":"2020-01-01","runtime":24}]}`)
		case "/tv/123/season/1":
			_, _ = io.WriteString(w, `{"season_number":1,"name":"第一季","overview":"正片","episodes":[{"episode_number":1,"name":"第一集"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	lib, err := db.UpsertLibrary("test", "/strm", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []*store.Item{
		{ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries, Name: "剧", SortName: "剧", TmdbID: "123"},
		{ID: "se0", LibraryID: lib.ID, ParentID: "s1", Type: store.TypeSeason, Name: "第 0 季", SortName: "Season 00", IndexNumber: 0, SeriesID: "s1", TmdbID: "123"},
		{ID: "e1", LibraryID: lib.ID, ParentID: "se0", Type: store.TypeEpisode, Name: "第 1 集", SortName: "0001", SeriesID: "s1", SeasonID: "se0", IndexNumber: 1, ParentIndexNumber: 0},
	} {
		if err := db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Config{
		DataDir:       t.TempDir(),
		TMDBAPIKeys:   []string{"test-key"},
		TMDBLanguage:  "zh-CN",
		TMDBBaseURL:   srv.URL,
		TMDBImageBase: srv.URL,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	return &seasonZeroFixture{scraper: New(cfg, db, log), db: db, mu: &mu, paths: &paths}
}

func (f *seasonZeroFixture) calledSeason(n string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range *f.paths {
		if p == "/tv/123/season/"+n {
			return true
		}
	}
	return false
}

func TestBackfillEpisodeSeasonZero(t *testing.T) {
	f := newSeasonZeroFixture(t)
	err := f.scraper.scrapeSeasonEpisodes(context.Background(),
		&store.Item{ID: "se0", ParentID: "s1", Type: store.TypeSeason, TmdbID: "123", IndexNumber: 0},
		map[string]struct{}{"e1": {}})
	if err != nil {
		t.Fatalf("backfill season 0: %v", err)
	}
	if !f.calledSeason("0") || f.calledSeason("1") {
		t.Fatalf("expected only season/0 request, paths = %v", *f.paths)
	}
	ep, err := f.db.GetItem("e1")
	if err != nil {
		t.Fatal(err)
	}
	if ep.Name != "特典一" || ep.ScrapedAt == 0 {
		t.Fatalf("episode not scraped: name=%q scraped_at=%d", ep.Name, ep.ScrapedAt)
	}
}

func TestScrapeSeasonZero(t *testing.T) {
	f := newSeasonZeroFixture(t)
	if err := f.scraper.scrapeSeason(context.Background(),
		&store.Item{ID: "se0", ParentID: "s1", Type: store.TypeSeason, TmdbID: "123", IndexNumber: 0}); err != nil {
		t.Fatalf("scrape season 0: %v", err)
	}
	if !f.calledSeason("0") || f.calledSeason("1") {
		t.Fatalf("expected only season/0 request, paths = %v", *f.paths)
	}
	se, err := f.db.GetItem("se0")
	if err != nil {
		t.Fatal(err)
	}
	if se.Overview != "ova 简介" {
		t.Fatalf("season overview = %q", se.Overview)
	}
	ep, _ := f.db.GetItem("e1")
	if ep.Name != "特典一" || ep.ScrapedAt == 0 {
		t.Fatalf("episode not scraped: name=%q scraped_at=%d", ep.Name, ep.ScrapedAt)
	}
}
