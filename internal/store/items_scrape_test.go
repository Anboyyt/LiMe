package store

import (
	"testing"
	"time"
)

func TestScrapeMetadataSurvivesRescan(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	movie, err := db.GetItem("m1")
	if err != nil {
		t.Fatal(err)
	}
	movie.TmdbID = "123"

	if err := db.UpdateItemMetadata("m1", MetadataUpdate{
		Overview:        "剧情简介",
		OriginalTitle:   "Original",
		PremiereDate:    "2020-01-02",
		CommunityRating: 8.5,
		RunTimeTicks:    120 * 60 * 10_000_000,
		ImagePath:       "images/poster.jpg",
		BackdropPath:    "images/backdrop.jpg",
	}); err != nil {
		t.Fatal(err)
	}

	// 模拟定时重扫：扫描器只认识路径信息，元数据字段都是空的。
	rescan := &Item{
		ID:             "m1",
		LibraryID:      "lib",
		ParentID:       "lib",
		Type:           TypeMovie,
		Name:           "电影",
		SortName:       "电影",
		TmdbID:         "123",
		ProductionYear: 2020,
	}
	if err := db.UpsertItem(rescan); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetItem("m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Overview != "剧情简介" || got.ImagePath != "images/poster.jpg" ||
		got.BackdropPath != "images/backdrop.jpg" || got.ScrapedAt == 0 {
		t.Fatalf("scrape metadata was wiped: %+v", got)
	}
	if got.CommunityRating != 8.5 || got.PremiereDate != "2020-01-02" || got.OriginalTitle != "Original" {
		t.Fatalf("scrape fields lost: %+v", got)
	}
	if got.RunTimeTicks != 120*60*10_000_000 {
		t.Fatalf("runtime lost: %d", got.RunTimeTicks)
	}
}

func TestMissingRuntimeQueries(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 电影：刮过但没时长才返回
	movie, _ := db.GetItem("m1")
	movie.TmdbID = "99"
	if err := db.UpsertItem(movie); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET run_time_ticks = 0 WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("m1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	if items, err := db.ItemsMissingRuntime(TypeMovie, before); err != nil || len(items) != 1 {
		t.Fatalf("missing runtime movies = %v %v", items, err)
	}
	if err := db.UpdateItemRuntime("m1", 90*60*10_000_000); err != nil {
		t.Fatal(err)
	}
	if items, err := db.ItemsMissingRuntime(TypeMovie, before); err != nil || len(items) != 0 {
		t.Fatalf("movie still missing runtime: %v %v", items, err)
	}

	// 剧集：按季聚合，只返回有已刮削单集的那一季
	series, _ := db.GetItem("s1")
	series.TmdbID = "123"
	if err := db.UpsertItem(series); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("s1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateEpisodeMetadata("e1", "第一集", "", "", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	seasons, err := db.SeriesSeasonsMissingEpisodeRuntime(before)
	if err != nil || len(seasons) != 1 || seasons[0].SeriesID != "s1" || seasons[0].Season != 1 {
		t.Fatalf("missing runtime seasons = %+v %v", seasons, err)
	}
	if err := db.UpdateItemRuntime("e1", 45*60*10_000_000); err != nil {
		t.Fatal(err)
	}
	if seasons, err := db.SeriesSeasonsMissingEpisodeRuntime(before); err != nil || len(seasons) != 0 {
		t.Fatalf("season still missing runtime: %+v %v", seasons, err)
	}
}

func TestEpisodeNameSurvivesRescan(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	if err := db.UpdateEpisodeMetadata("e1", "真正的剧集标题", "简介", "2020-01-01", 24*60*10_000_000, "images/p.jpg", "images/b.jpg", ""); err != nil {
		t.Fatal(err)
	}

	// 重扫会重新写入「第 1 集」
	if err := db.UpsertItem(&Item{
		ID:                "e1",
		LibraryID:         "lib",
		ParentID:          "se1",
		Type:              TypeEpisode,
		Name:              "第 1 集",
		SortName:          "0001",
		SeriesID:          "s1",
		SeasonID:          "se1",
		IndexNumber:       1,
		ParentIndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetItem("e1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "真正的剧集标题" {
		t.Fatalf("episode name = %q, want tmdb name", got.Name)
	}
}

func TestGenresAndPeopleSurviveRescan(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	movie, _ := db.GetItem("m1")
	movie.TmdbID = "123"
	if err := db.UpsertItem(movie); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("m1", MetadataUpdate{
		Genres: `["动画","奇幻"]`,
		People: `[{"Name":"张三","Role":"主角","Type":"Actor"}]`,
	}); err != nil {
		t.Fatal(err)
	}

	// 重扫只带路径信息，不能把类型/演员冲掉
	if err := db.UpsertItem(&Item{
		ID: "m1", LibraryID: "lib", ParentID: "lib", Type: TypeMovie,
		Name: "电影", SortName: "电影", TmdbID: "123",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetItem("m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Genres != `["动画","奇幻"]` || got.People == "" {
		t.Fatalf("metadata wiped: %+v", got)
	}

	names, err := db.GenreNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "动画" || names[1] != "奇幻" {
		t.Fatalf("genre names = %v", names)
	}
}

func TestMissingEpisodeStills(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	series, _ := db.GetItem("s1")
	series.TmdbID = "123"
	if err := db.UpsertItem(series); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("s1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}
	// e1 已刮削但还在共用季海报；e2 没刮过，不参与
	if err := db.UpdateEpisodeMetadata("e1", "第一集", "", "", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	seasons, err := db.SeriesSeasonsMissingEpisodeStill(before)
	if err != nil || len(seasons) != 1 || seasons[0].SeriesID != "s1" || seasons[0].Season != 1 {
		t.Fatalf("missing stills = %+v %v", seasons, err)
	}

	if err := db.UpdateItemImage("e1", "images/still.jpg"); err != nil {
		t.Fatal(err)
	}
	if seasons, err := db.SeriesSeasonsMissingEpisodeStill(before); err != nil || len(seasons) != 0 {
		t.Fatalf("still still missing: %+v %v", seasons, err)
	}
}

// TMDB 本身没有评分/类型/时长的条目，尝试一次后应退出补齐队列，
// 不能每天重复请求。
func TestMissingMetadataConverges(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	movie, _ := db.GetItem("m1")
	movie.TmdbID = "99"
	if err := db.UpsertItem(movie); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET run_time_ticks = 0, official_rating = '', genres = '' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("m1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	if items, err := db.ItemsMissingOfficialRating(TypeMovie, before); err != nil || len(items) != 1 {
		t.Fatalf("missing ratings = %v %v", items, err)
	}
	if items, err := db.ItemsMissingGenres(TypeMovie, before); err != nil || len(items) != 1 {
		t.Fatalf("missing genres = %v %v", items, err)
	}
	if items, err := db.ItemsMissingRuntime(TypeMovie, before); err != nil || len(items) != 1 {
		t.Fatalf("missing runtime = %v %v", items, err)
	}

	// TMDB 三个字段都返回空，仍要记一次尝试
	if err := db.UpdateItemRating("m1", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemRuntime("m1", 0); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("m1", MetadataUpdate{MetadataChecked: true}); err != nil {
		t.Fatal(err)
	}

	if items, err := db.ItemsMissingOfficialRating(TypeMovie, before); err != nil || len(items) != 0 {
		t.Fatalf("ratings not converged: %v %v", items, err)
	}
	if items, err := db.ItemsMissingGenres(TypeMovie, before); err != nil || len(items) != 0 {
		t.Fatalf("genres not converged: %v %v", items, err)
	}
	if items, err := db.ItemsMissingRuntime(TypeMovie, before); err != nil || len(items) != 0 {
		t.Fatalf("runtime not converged: %v %v", items, err)
	}

	// 过了重试间隔还会再试一次
	later := time.Now().Add(31 * 24 * time.Hour).Unix()
	if items, err := db.ItemsMissingGenres(TypeMovie, later); err != nil || len(items) != 1 {
		t.Fatalf("genres should retry after interval: %v %v", items, err)
	}
}

// 季级补缺（单集剧照/时长）同样要收敛，并带回季 ID 用于标记。
func TestMissingSeasonWorkConverges(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	series, _ := db.GetItem("s1")
	series.TmdbID = "123"
	if err := db.UpsertItem(series); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateItemMetadata("s1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}
	// e1 已刮削但无剧照、无时长
	if err := db.UpdateEpisodeMetadata("e1", "第一集", "", "", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	stillSeasons, err := db.SeriesSeasonsMissingEpisodeStill(before)
	if err != nil || len(stillSeasons) != 1 || stillSeasons[0].SeasonID != "se1" {
		t.Fatalf("missing stills = %+v %v", stillSeasons, err)
	}
	runtimeSeasons, err := db.SeriesSeasonsMissingEpisodeRuntime(before)
	if err != nil || len(runtimeSeasons) != 1 || runtimeSeasons[0].SeasonID != "se1" {
		t.Fatalf("missing runtime = %+v %v", runtimeSeasons, err)
	}

	if err := db.MarkSeasonStillsChecked("se1"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkSeasonRuntimeChecked("se1"); err != nil {
		t.Fatal(err)
	}

	if seasons, err := db.SeriesSeasonsMissingEpisodeStill(before); err != nil || len(seasons) != 0 {
		t.Fatalf("stills not converged: %+v %v", seasons, err)
	}
	if seasons, err := db.SeriesSeasonsMissingEpisodeRuntime(before); err != nil || len(seasons) != 0 {
		t.Fatalf("runtime not converged: %+v %v", seasons, err)
	}
}

func TestItemsForScrape(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	// 没有 TMDB ID 的不参与刮削
	if items, err := db.ItemsForScrape(TypeMovie); err != nil || len(items) != 0 {
		t.Fatalf("unexpected items: %v %v", items, err)
	}

	movie, _ := db.GetItem("m1")
	movie.TmdbID = "123"
	if err := db.UpsertItem(movie); err != nil {
		t.Fatal(err)
	}

	items, err := db.ItemsForScrape(TypeMovie)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("ItemsForScrape = %+v, want m1", items)
	}

	// 刮过的不再返回
	if err := db.UpdateItemMetadata("m1", MetadataUpdate{Overview: "x"}); err != nil {
		t.Fatal(err)
	}
	if items, err := db.ItemsForScrape(TypeMovie); err != nil || len(items) != 0 {
		t.Fatalf("already scraped item returned: %v %v", items, err)
	}
}

func TestEpisodeByNumber(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	it, err := db.EpisodeByNumber("s1", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "e2" {
		t.Fatalf("got %s, want e2", it.ID)
	}

	if _, err := db.EpisodeByNumber("s1", 1, 99); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
