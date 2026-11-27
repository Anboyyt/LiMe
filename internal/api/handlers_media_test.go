package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/config"
	"lime/internal/dto"
	"lime/internal/store"
)

func TestSourceDisplayName(t *testing.T) {
	// 6.1GB / 45 分钟 ≈ 18.1 Mbps
	s := &store.MediaSource{Size: 6_126_692_694}
	streams := []*store.MediaStream{{Type: "Video", Height: 2160, VideoRange: "DoVi", FrameRate: 23.976}}
	bitrate := sourceBitrate(s, 45*60*10_000_000)
	if bitrate < 18_000_000 || bitrate > 18_300_000 {
		t.Fatalf("bitrate = %d", bitrate)
	}
	got := sourceDisplayName(s, streams, bitrate)
	if !strings.HasPrefix(got, "DV · 2160p · ") || !strings.HasSuffix(got, " Mbps") {
		t.Fatalf("display name = %q", got)
	}
	// 帧率四舍五入成整数，大小和码率同时显示。
	if !strings.Contains(got, "24fps") || !strings.Contains(got, "6.1GB") {
		t.Fatalf("display name missing fps/size: %q", got)
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

// Yamby 跟 302 不稳，视频流要由 LiMe 中转；其他客户端保持 302。
func TestYambyStreamIsProxied(t *testing.T) {
	var gotRange, gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		gotUA = r.UserAgent()
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.Header().Set("Content-Type", "video/x-matroska")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()

	f := newUserFixtureWithConfig(t, func(c *config.Config) { c.ProxyYamby = true })
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm",
		URL: upstream.URL + "/movie.mkv", Container: "mkv", FileName: "movie.mkv",
	}); err != nil {
		t.Fatal(err)
	}

	const path = "/Videos/m1/stream?static=true&MediaSourceId=src1"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	req.Header.Set("User-Agent", "Yamby/2.1.0.11(Android 16)")
	req.Header.Set("Range", "bytes=0-3")
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "data" {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if gotRange != "bytes=0-3" || !strings.HasPrefix(gotUA, "Yamby/") {
		t.Fatalf("upstream range/ua = %q / %q", gotRange, gotUA)
	}

	// 非 Yamby 仍然走 302。
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	req.Header.Set("User-Agent", "VidHub/3.0.3")
	rr = httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("VidHub status = %d, want 302", rr.Code)
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

// PanSave 的 Emby 反代靠 Item 详情里的 media_source_path 判断片源是不是
// 它自己的 /strm 源，只有那种形状才启用 115 复制同播。开启 MediaPathStrm
// 后 Path 必须回 STRM 原始地址；Yamby/爆米花仍要相对地址，否则拼坏。
func TestMediaPathStrmReturnsRawStrmURL(t *testing.T) {
	f := newUserFixtureWithConfig(t, func(c *config.Config) { c.MediaPathStrm = "true" })
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
	const raw = "http://192.168.5.133:16868/strm/115/drive?drive_id=5&pickcode=abc&file_name=x.mp4"
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm", URL: raw,
		Container: "mp4", FileName: "m1.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/Items/m1?Fields=MediaSources",
		"/Shows/m1/Episodes",
	} {
		rr := f.do(http.MethodGet, path, nil, f.adminTok)
		if rr.Code != http.StatusOK {
			// /Shows/{id}/Episodes 对电影不适用，跳过即可。
			continue
		}
		var res struct {
			MediaSources []struct {
				Path            string `json:"Path"`
				DirectStreamUrl string `json:"DirectStreamUrl"`
			} `json:"MediaSources"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.MediaSources) == 0 {
			continue
		}
		// Path 给 PanSave 观察，必须是 /strm 原文。
		if res.MediaSources[0].Path != raw {
			t.Fatalf("%s path = %q, want raw strm url", path, res.MediaSources[0].Path)
		}
		// DirectStreamUrl 才是客户端播放地址，必须留在 LiMe 的端点上，
		// 否则客户端直接打 16868 会绕过反代、拿不到副本。
		if !strings.HasPrefix(res.MediaSources[0].DirectStreamUrl, "http://example.com/Videos/m1/stream") {
			t.Fatalf("%s direct url = %q", path, res.MediaSources[0].DirectStreamUrl)
		}
	}

	rr := f.do(http.MethodPost, "/Items/m1/PlaybackInfo", map[string]any{}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("playback info = %d: %s", rr.Code, rr.Body.String())
	}
	var info dto.PlaybackInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.MediaSources) != 1 || info.MediaSources[0].Path != raw {
		t.Fatalf("playback info path = %+v", info.MediaSources)
	}
	if !strings.HasPrefix(info.MediaSources[0].DirectStreamUrl, "http://example.com/Videos/m1/stream") {
		t.Fatalf("playback direct url = %q", info.MediaSources[0].DirectStreamUrl)
	}

	// Yamby 会拼接服务器地址，MediaPathStrm 也必须给它相对路径。
	rr = f.doUA(http.MethodPost, "/Items/m1/PlaybackInfo", map[string]any{}, "Yamby/2.1.0", f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("yamby playback info = %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if got := info.MediaSources[0].Path; !strings.HasPrefix(got, "/Videos/m1/stream") {
		t.Fatalf("yamby path = %q", got)
	}
}

// auto 模式：只有内网反代（X-Forwarded-Host 是私有地址）才把 Path 换成
// STRM 原文。Cloudflare 隧道转发的是公网域名，不能跟着换，否则外网客户端
// 会拿到连不上的内网地址。
func TestMediaPathStrmAutoByForwardedHost(t *testing.T) {
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
	const raw = "http://192.168.5.133:16868/strm/115/drive?pickcode=abc"
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm", URL: raw,
		Container: "mp4", FileName: "m1.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	get := func(xfh string) string {
		req := httptest.NewRequest(http.MethodGet, "/Items/m1?Fields=MediaSources", nil)
		req.Header.Set("X-Emby-Token", f.adminTok)
		if xfh != "" {
			req.Header.Set("X-Forwarded-Host", xfh)
		}
		rr := httptest.NewRecorder()
		f.env.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		var res struct {
			MediaSources []struct {
				Path            string `json:"Path"`
				DirectStreamUrl string `json:"DirectStreamUrl"`
			} `json:"MediaSources"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.MediaSources) == 0 {
			t.Fatal("no media sources")
		}
		return res.MediaSources[0].Path
	}

	// 直连：保持 LiMe 地址。
	if got := get(""); strings.HasPrefix(got, "http://192.168.5.133:16868") {
		t.Fatalf("direct path = %q, want LiMe url", got)
	}
	// PanSave 内网反代：换成 STRM 原文。
	if got := get("192.168.5.133:1888"); got != raw {
		t.Fatalf("proxy path = %q, want %q", got, raw)
	}
	// Cloudflare 隧道（公网域名）：不能换。
	if got := get("lime.974517.xyz"); strings.HasPrefix(got, "http://192.168.5.133:16868") {
		t.Fatalf("public proxy path = %q, want LiMe url", got)
	}
}

// InternalURL 是给没走 MediaPathStrm 的场景用的（PanSave 探测上游）。
// 一旦请求来自内网反代并启用了 MediaPathStrm，播放地址必须留在反代入口
// （X-Forwarded-Host），否则客户端直连 LiMe 端口会绕过反代，拿不到副本。
func TestMediaPathStrmKeepsProxyHostOverInternalURL(t *testing.T) {
	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.InternalURL = "http://192.168.5.133:888"
	})
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
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm",
		URL: "http://192.168.5.133:16868/strm/115/drive?pickcode=abc",
		Container: "mp4", FileName: "m1.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/Items/m1?Fields=MediaSources", nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	req.Header.Set("X-Forwarded-Host", "192.168.5.133:1888")
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var res struct {
		MediaSources []struct {
			Path            string `json:"Path"`
			DirectStreamUrl string `json:"DirectStreamUrl"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.MediaSources) != 1 {
		t.Fatalf("sources = %d", len(res.MediaSources))
	}
	if got := res.MediaSources[0].DirectStreamUrl; !strings.HasPrefix(got, "http://192.168.5.133:1888/") {
		t.Fatalf("direct url = %q, want proxy host", got)
	}
}

// 私网直连 LiMe（没有 X-Forwarded-Host）时，只要配了反代入口，也要把
// 播放地址引导过去：客户端走 886 的请求根本不经过 PanSave，拿不到 device_id，
// 只有把 DirectStreamUrl 指向反代端口才能分配副本。
func TestMediaPathStrmGuidesPrivateDirectToProxy(t *testing.T) {
	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.InternalURL = "http://192.168.1.10:1886"
	})
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
	const raw = "http://192.168.1.10:16868/strm/115/drive?pickcode=abc"
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm", URL: raw,
		Container: "mp4", FileName: "m1.mp4",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:886/Items/m1?Fields=MediaSources", nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var res struct {
		MediaSources []struct {
			Path            string `json:"Path"`
			DirectStreamUrl string `json:"DirectStreamUrl"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.MediaSources) != 1 {
		t.Fatalf("sources = %d", len(res.MediaSources))
	}
	if got := res.MediaSources[0].Path; got != raw {
		t.Fatalf("path = %q, want %q", got, raw)
	}
	if got := res.MediaSources[0].DirectStreamUrl; !strings.HasPrefix(got, "http://192.168.1.10:1886/") {
		t.Fatalf("direct url = %q, want proxy entry", got)
	}

	// 外网（公网 Host）不引导，保持原行为。
	req = httptest.NewRequest(http.MethodGet, "http://lime.example.com/Items/m1?Fields=MediaSources", nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	rr = httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public status = %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if got := res.MediaSources[0].Path; strings.HasPrefix(got, "http://192.168.1.10:16868") {
		t.Fatalf("public path = %q, must stay on LiMe", got)
	}
}

func TestSourceDisplayNameFallback(t *testing.T) {
	s := &store.MediaSource{VersionName: "2160p · HDR · HEVC · 5.6GB"}
	if got := sourceDisplayName(s, nil, 0); got != s.VersionName {
		t.Fatalf("fallback = %q", got)
	}
}

// MKV 内嵌封面不能作为 Video 轨道返回，否则客户端会报
// "none are playable by this device"（Yamby/ExoPlayer）。
func TestEmbeddedCoverNotReturnedAsVideo(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm",
		URL: "http://example", Container: "mkv", FileName: "m1.mkv",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.ReplaceMediaStreams("src1", []store.MediaStream{
		{Index: 0, Type: "Video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 24},
		{Index: 1, Type: "Audio", Codec: "aac", Channels: 2},
		{Index: 2, Type: "Video", Codec: "mjpeg", Width: 640, Height: 360},
	}); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodPost, "/Items/m1/PlaybackInfo", map[string]any{}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var info dto.PlaybackInfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.MediaSources) != 1 {
		t.Fatalf("sources = %d", len(info.MediaSources))
	}
	streams := info.MediaSources[0].MediaStreams
	// 封面轨道直接不返回，只留真正的视频/音频。
	if len(streams) != 2 {
		t.Fatalf("streams = %+v", streams)
	}
	if streams[0].Type != "Video" || streams[0].Codec != "h264" {
		t.Fatalf("main video = %+v", streams[0])
	}
	for _, st := range streams {
		if st.Type == "Video" && st.Codec == "mjpeg" {
			t.Fatalf("cover leaked into video tracks: %+v", st)
		}
	}
}
