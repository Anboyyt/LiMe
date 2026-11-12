package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

// 剧集列表（/Shows/{id}/Episodes）要带轨道信息：SenPlayer 这类客户端
// 打开单集详情时直接用列表里这条渲染「媒体信息」，只给空 MediaStreams
// 会让剧集页永远只有文件名、没有视频/音频参数。
func TestShowEpisodesCarriesMediaStreams(t *testing.T) {
	f := newUserFixture(t)

	lib, err := f.env.db.UpsertLibrary("动漫", "/strm/动漫", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	items := []*store.Item{
		{ID: "series1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries, Name: "剧集", SortName: "剧集"},
		{ID: "season1", LibraryID: lib.ID, ParentID: "series1", SeriesID: "series1", Type: store.TypeSeason, Name: "Season 1", SortName: "Season 1"},
		{ID: "ep1", LibraryID: lib.ID, ParentID: "season1", SeriesID: "series1", SeasonID: "season1",
			Type: store.TypeEpisode, Name: "第一集", SortName: "第一集"},
	}
	for _, it := range items {
		if err := f.env.db.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src-ep1", ItemID: "ep1", StrmPath: "/strm/动漫/剧集/ep1.strm", URL: "http://example",
		Container: "mp4", FileName: "ep1.mp4", VersionName: "2160p",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.ReplaceMediaStreams("src-ep1", []store.MediaStream{
		{Index: 0, Type: "Video", Codec: "h264", Width: 3840, Height: 2160, Profile: "High",
			ColorSpace: "bt709", PixelFormat: "yuv420p", FrameRate: 23.998},
		{Index: 1, Type: "Audio", Codec: "aac", Channels: 2, SampleRate: 48000, Language: "chi"},
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Shows/series1/Episodes", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	var res struct {
		Items []struct {
			Id           string `json:"Id"`
			MediaSources []struct {
				Name         string `json:"Name"`
				MediaStreams []struct {
					Type      string   `json:"Type"`
					Codec     string   `json:"Codec"`
					Height    int      `json:"Height"`
					Profile   string   `json:"Profile"`
					Channels  int      `json:"Channels"`
					FrameRate *float64 `json:"RealFrameRate"`
				} `json:"MediaStreams"`
			} `json:"MediaSources"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d", len(res.Items))
	}
	srcs := res.Items[0].MediaSources
	if len(srcs) != 1 {
		t.Fatalf("media sources = %d", len(srcs))
	}
	if len(srcs[0].MediaStreams) != 2 {
		t.Fatalf("media streams = %+v", srcs[0].MediaStreams)
	}
	v, a := srcs[0].MediaStreams[0], srcs[0].MediaStreams[1]
	if v.Type != "Video" || v.Codec != "h264" || v.Height != 2160 || v.Profile != "High" {
		t.Fatalf("video stream = %+v", v)
	}
	if v.FrameRate == nil || *v.FrameRate != 23.998 {
		t.Fatalf("video frame rate = %v", v.FrameRate)
	}
	if a.Type != "Audio" || a.Codec != "aac" || a.Channels != 2 {
		t.Fatalf("audio stream = %+v", a)
	}
	// 有轨道时 Name 用「4K · 2160p …」这种描述，而不是 STRM 文件名。
	if srcs[0].Name == "" || srcs[0].Name == "2160p" {
		t.Fatalf("media source name = %q", srcs[0].Name)
	}
}
