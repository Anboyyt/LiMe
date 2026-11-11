package strm

import "testing"

func TestParsePath(t *testing.T) {
	cases := []struct {
		name       string
		rel        string
		wantOK     bool
		wantTitle  string
		wantYear   int
		wantSeason int
		wantTmdb   string
	}{
		{
			name:      "电影目录",
			rel:       "逐爱天堂 Angel (2007) {tmdb-11801}/逐爱天堂 Angel (2007) {tmdb-11801} - 1080p 25fps SDR AAC2.0/逐爱天堂.Angel.2007.1080p.25fps.SDR.H.265.8bit.AAC2.0.strm",
			wantOK:    true,
			wantTitle: "逐爱天堂 Angel",
			wantYear:  2007,
			wantTmdb:  "11801",
		},
		{
			name:       "剧集目录带季",
			rel:        "二炮手 (2014) {tmdb-95750}/Season 1 - 1080p SDR/二炮手.2014.S01E03.1080p.25fps.SDR.H.264.8bit.AAC2.0.strm",
			wantOK:     true,
			wantTitle:  "二炮手",
			wantYear:   2014,
			wantSeason: 1,
			wantTmdb:   "95750",
		},
		{
			name:       "多季",
			rel:        "汪汪的太空任务 Dogs in Space (2021) {tmdb-136752}/Season 2 - 1080p HDR10/x.strm",
			wantOK:     true,
			wantTitle:  "汪汪的太空任务 Dogs in Space",
			wantYear:   2021,
			wantSeason: 2,
			wantTmdb:   "136752",
		},
		{
			name:      "无规整目录",
			rel:       "杂项目录/file.strm",
			wantOK:    false,
			wantTitle: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParsePath(tc.rel)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			checkStr(t, "Title", got.Title, tc.wantTitle)
			checkInt(t, "Year", got.Year, tc.wantYear)
			checkInt(t, "Season", got.Season, tc.wantSeason)
			checkStr(t, "TmdbID", got.TmdbID, tc.wantTmdb)
		})
	}
}
