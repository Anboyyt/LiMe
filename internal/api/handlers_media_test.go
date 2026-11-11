package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

func TestSourceDisplayName(t *testing.T) {
	// 6.1GB / 45 分钟 ≈ 18.1 Mbps
	s := &store.MediaSource{Size: 6_126_692_694}
	streams := []*store.MediaStream{{Type: "Video", Height: 2160, VideoRange: "DoVi"}}
	bitrate := sourceBitrate(s, 45*60*10_000_000)
	if bitrate < 18_000_000 || bitrate > 18_300_000 {
		t.Fatalf("bitrate = %d", bitrate)
	}
	got := sourceDisplayName(s, streams, bitrate)
	if !strings.HasPrefix(got, "DV · 2160p · ") || !strings.HasSuffix(got, " Mbps") {
		t.Fatalf("display name = %q", got)
	}

	sdr := &store.MediaSource{Size: 1_900_000_000}
	streams = []*store.MediaStream{{Type: "Video", Height: 1080, VideoRange: ""}}
	got = sourceDisplayName(sdr, streams, sourceBitrate(sdr, 45*60*10_000_000))
	if !strings.HasPrefix(got, "SDR · 1080p · ") {
		t.Fatalf("sdr display name = %q", got)
	}
}

func TestSourceBitrateFallback(t *testing.T) {
	s := &store.MediaSource{Size: 1_000_000_000}
	if got := sourceBitrate(s, 0); got != 0 {
		t.Fatalf("no runtime bitrate = %d, want 0", got)
	}
	// 片源自身时长优先
	s.DurationTicks = 10 * 60 * 10_000_000
	if got := sourceBitrate(s, 60*60*10_000_000); got != 13_333_333 {
		t.Fatalf("bitrate with source duration = %d", got)
	}
}

func TestSubtitleStream(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	strmPath := filepath.Join(dir, "movie.strm")
	if err := os.WriteFile(strmPath, []byte("http://example"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "movie.chs.ass"), []byte("[Script Info]\nTitle: test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: strmPath, URL: "http://example",
		Container: "mkv", FileName: "movie.mkv",
	}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/Videos/m1/src1/Subtitles/10000/Stream.ass",
		"/Videos/m1/Subtitles/10000/Stream.ass",
		"/Videos/m1/src1/Subtitles/10000/0/Stream.ass",
	} {
		rr := f.do(http.MethodGet, path, nil, f.adminTok)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "Script Info") ||
			!strings.HasPrefix(rr.Header().Get("Content-Type"), "text/x-ssa") {
			t.Fatalf("%s body/type = %q / %q", path, rr.Body.String(), rr.Header().Get("Content-Type"))
		}
	}

	for _, path := range []string{
		"/Videos/m1/src1/Subtitles/10001/Stream.ass", // 超出现有外挂字幕数量
		"/Videos/m1/src1/Subtitles/0/Stream.ass",     // 容器轨道序号无法从这里取
	} {
		rr := f.do(http.MethodGet, path, nil, f.adminTok)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, rr.Code)
		}
	}

	// 探测完成后容器轨道会多出来，外挂字幕序号不能跟着往后挪。
	if err := f.env.db.ReplaceMediaStreams("src1", []store.MediaStream{
		{Index: 0, Type: "Video", Codec: "h264"},
		{Index: 1, Type: "Audio", Codec: "aac", Channels: 2},
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodPost, "/Items/m1/PlaybackInfo", map[string]any{}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("playback info = %d: %s", rr.Code, rr.Body.String())
	}
	var info dto.PlaybackInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.MediaSources) != 1 || len(info.MediaSources[0].MediaStreams) == 0 {
		t.Fatalf("playback sources = %+v", info)
	}
	var sub *dto.MediaStreamDto
	for i := range info.MediaSources[0].MediaStreams {
		if info.MediaSources[0].MediaStreams[i].Type == "Subtitle" {
			sub = &info.MediaSources[0].MediaStreams[i]
		}
	}
	if sub == nil || !sub.IsExternal || !strings.Contains(sub.DeliveryUrl, "/Subtitles/10000/Stream.ass") ||
		sub.Index != 10000 || sub.Language != "chi" {
		t.Fatalf("subtitle stream = %+v", sub)
	}
}

func TestPlaybackInfoRequestedSourceFirst(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"src1", "src2"} {
		if err := f.env.db.UpsertMediaSource(&store.MediaSource{
			ID: id, ItemID: "m1", StrmPath: "/strm/" + id + ".strm",
			URL: "http://example/" + id, Container: "mkv", FileName: id + ".mkv",
			SortRank: 0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rr := f.do(http.MethodPost, "/Items/m1/PlaybackInfo",
		map[string]string{"MediaSourceId": "src2"}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("playback info = %d: %s", rr.Code, rr.Body.String())
	}
	var info dto.PlaybackInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.MediaSources) != 2 || info.MediaSources[0].Id != "src2" {
		t.Fatalf("requested source not first: %+v", info.MediaSources)
	}
}

func TestSourceDisplayNameFallback(t *testing.T) {
	s := &store.MediaSource{VersionName: "2160p · HDR · HEVC · 5.6GB"}
	if got := sourceDisplayName(s, nil, 0); got != s.VersionName {
		t.Fatalf("fallback = %q", got)
	}
}
