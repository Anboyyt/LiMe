package api

import (
	"strings"
	"testing"

	"lime/internal/store"
)

func TestEpisodeRanges(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{[]int{11}, "E11"},
		{[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, "E01-E10"},
		{[]int{1, 2, 3, 5, 7, 8}, "E01-E03、E05、E07-E08"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := episodeRanges(c.in); got != c.want {
			t.Errorf("episodeRanges(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIngestGroupsEpisodesBySeason(t *testing.T) {
	env := newTestEnv(t)
	lib, err := env.db.UpsertLibrary("综艺", "/strm/综艺", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	series := &store.Item{
		ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries,
		Name: "女神配对计划", SortName: "女神配对计划", ProductionYear: 2025,
		TmdbID: "293060", People: `[{"Name":"梁敏巧"},{"Name":"叶蒨文"},{"Name":"关嘉敏"},{"Name":"罗毓仪"},{"Name":"李芷晴"},{"Name":"第六人"}]`,
	}
	if err := env.db.UpsertItem(series); err != nil {
		t.Fatal(err)
	}
	season := &store.Item{
		ID: "se1", LibraryID: lib.ID, ParentID: "s1", Type: store.TypeSeason,
		Name: "第 2 季", SortName: "Season 02", SeriesID: "s1", IndexNumber: 2,
	}
	if err := env.db.UpsertItem(season); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3} {
		if err := env.db.UpsertItem(&store.Item{
			ID: "e" + string(rune('0'+n)), LibraryID: lib.ID, ParentID: "se1",
			Type: store.TypeEpisode, Name: "第 " + string(rune('0'+n)) + " 集",
			SeriesID: "s1", SeasonID: "se1", ParentIndexNumber: 2, IndexNumber: n,
		}); err != nil {
			t.Fatal(err)
		}
	}

	pending := []store.PendingIngest{
		{ID: "src1", ItemID: "e1", StrmPath: "/strm/综艺/华语综艺/女神配对计划/S02E01.strm",
			FileName: "女神配对计划.2025.S02E01.1080p.WEB-DL.SDR.25fps.MyTVSuper.H.264-ADWeb.mp4",
			Cloud:    "115", Size: 1 << 30, IngestedAt: 100},
		{ID: "src2", ItemID: "e2", StrmPath: "/strm/综艺/华语综艺/女神配对计划/S02E02.strm",
			FileName: "女神配对计划.2025.S02E02.1080p.WEB-DL.SDR.25fps.MyTVSuper.H.264-ADWeb.mp4",
			Cloud:    "115", Size: 1 << 30, IngestedAt: 101},
		{ID: "src3", ItemID: "e3", StrmPath: "/strm/综艺/华语综艺/女神配对计划/S02E03.strm",
			FileName: "女神配对计划.2025.S02E03.1080p.WEB-DL.SDR.25fps.MyTVSuper.H.264-ADWeb.mp4",
			Cloud:    "115", Size: 1 << 30, IngestedAt: 102},
	}

	groups, orphans := env.api.groupIngestSources(pending)
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v", orphans)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	msg := env.api.formatIngestMessage(groups[0], map[string]int{"se1": 10})
	for _, want := range []string{
		"女神配对计划 (2025) S02 E01-E03",
		"梁敏巧 / 叶蒨文 / 关嘉敏 / 罗毓仪 / 李芷晴 等",
		"TMDB-293060",
		"规则：115",
		"来源：115网盘",
		"分类：综艺 / 华语综艺",
		"版本：1080p WEB-DL SDR 25fps",
		"文件：3个",
		"大小：3.00G",
		"平台：MyTVSuper",
		"组名：ADWeb",
		"本季：S02 共10集",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestIngestMoviesStaySeparate(t *testing.T) {
	env := newTestEnv(t)
	lib, err := env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
			Name: id, SortName: id, ProductionYear: 2026,
		}); err != nil {
			t.Fatal(err)
		}
	}
	pending := []store.PendingIngest{
		{ID: "s1", ItemID: "m1", StrmPath: "/strm/电影/欧美/m1.strm", FileName: "m1.2026.1080p.WEB-DL.mkv", IngestedAt: 1},
		{ID: "s2", ItemID: "m2", StrmPath: "/strm/电影/欧美/m2.strm", FileName: "m2.2026.1080p.WEB-DL.mkv", IngestedAt: 1},
	}
	groups, _ := env.api.groupIngestSources(pending)
	if len(groups) != 2 {
		t.Fatalf("movie groups = %d, want 2", len(groups))
	}
	for _, g := range groups {
		msg := env.api.formatIngestMessage(g, nil)
		if strings.Contains(msg, "本季：") {
			t.Errorf("movie message should not contain season line:\n%s", msg)
		}
	}
}

func TestIngestCategoryFallsBackToLibrary(t *testing.T) {
	lib := &store.Library{Name: "电影", Path: "/strm/电影"}
	if got := ingestCategory(lib, "/strm/电影/沙丘2/电影.strm", "沙丘2"); got != "电影" {
		t.Fatalf("category = %q, want 电影", got)
	}
	if got := ingestCategory(lib, "/strm/电影/欧美/沙丘2/电影.strm", "沙丘2"); got != "电影 / 欧美" {
		t.Fatalf("category = %q, want 电影 / 欧美", got)
	}
}
