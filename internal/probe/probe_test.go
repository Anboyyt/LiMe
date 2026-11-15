package probe

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"lime/internal/store"
)

const sampleJSON = `{
  "streams": [
    {
      "index": 0, "codec_name": "hevc", "codec_type": "video",
      "width": 3840, "height": 2160, "profile": "Main 10", "level": 156,
      "pix_fmt": "yuv420p10le", "avg_frame_rate": "24000/1001",
      "color_primaries": "bt2020", "color_transfer": "smpte2084",
      "color_space": "bt2020nc", "field_order": "progressive",
      "display_aspect_ratio": "16:9",
      "disposition": {"default": 1, "forced": 0}
    },
    {
      "index": 1, "codec_name": "eac3", "codec_type": "audio",
      "bit_rate": "750000", "channels": 6, "channel_layout": "5.1",
      "sample_rate": "48000", "tags": {"language": "eng", "title": "English EAC3 5.1"},
      "disposition": {"default": 1, "forced": 0}
    },
    {
      "index": 2, "codec_name": "ass", "codec_type": "subtitle",
      "tags": {"language": "chi", "title": "chs&eng"},
      "disposition": {"default": 1, "forced": 0}
    }
  ],
  "format": {"duration": "7200.5"}
}`

func TestConvert(t *testing.T) {
	var res ffprobeResult
	if err := json.Unmarshal([]byte(sampleJSON), &res); err != nil {
		t.Fatal(err)
	}
	streams := convert(&res)
	if len(streams) != 3 {
		t.Fatalf("streams = %d, want 3", len(streams))
	}

	v := streams[0]
	if v.Type != "Video" || v.Codec != "hevc" || v.Width != 3840 || v.Height != 2160 {
		t.Fatalf("video = %+v", v)
	}
	if v.BitDepth != 10 || v.FrameRate != 23.976 || v.VideoRange != "HDR10" {
		t.Fatalf("video params = depth %d fps %v range %q", v.BitDepth, v.FrameRate, v.VideoRange)
	}
	if v.Profile != "Main 10" || v.Level != 156 || v.AspectRatio != "16:9" {
		t.Fatalf("video profile = %+v", v)
	}
	if v.ColorPrimaries != "bt2020" || v.ColorTransfer != "smpte2084" || v.ColorSpace != "bt2020nc" {
		t.Fatalf("color = %+v", v)
	}

	a := streams[1]
	if a.Type != "Audio" || a.Channels != 6 || a.ChannelLayout != "5.1" || a.SampleRate != 48000 {
		t.Fatalf("audio = %+v", a)
	}
	if a.BitRate != 750000 || a.Language != "eng" || a.Title != "English EAC3 5.1" || !a.IsDefault {
		t.Fatalf("audio tags = %+v", a)
	}

	s := streams[2]
	if s.Type != "Subtitle" || s.Codec != "ass" || s.Language != "chi" || !s.IsDefault {
		t.Fatalf("subtitle = %+v", s)
	}
}

func TestVideoRangeDoVi(t *testing.T) {
	var s ffStream
	s.ColorTransfer = "smpte2084"
	s.SideDataList = append(s.SideDataList, struct {
		SideDataType string `json:"side_data_type"`
	}{SideDataType: "DOVI configuration record"})
	if got := videoRange(s); got != "DoVi" {
		t.Fatalf("video range = %q, want DoVi", got)
	}
}

func TestVideoBitDepthFallback(t *testing.T) {
	cases := map[string]int{
		"yuv420p":     8,
		"yuv420p10le": 10,
		"p010le":      10,
		"yuv420p12le": 12,
		"":            0,
	}
	for pix, want := range cases {
		if got := videoBitDepth(ffStream{PixFmt: pix}); got != want {
			t.Errorf("bit depth %q = %d, want %d", pix, got, want)
		}
	}
}

func TestParseWindow(t *testing.T) {
	s, e, ok := ParseWindow("03:00-06:00")
	if !ok || s != 180 || e != 360 {
		t.Fatalf("got %d %d %v", s, e, ok)
	}
	if _, _, ok := ParseWindow(""); ok {
		t.Fatal("empty should be disabled")
	}
	if _, _, ok := ParseWindow("25:00-06:00"); ok {
		t.Fatal("invalid hour accepted")
	}
}

func TestWarmupWindowOpen(t *testing.T) {
	c := WarmupConfig{StartMin: 180, EndMin: 360} // 03:00-06:00
	at := func(h, m int) bool {
		return c.open(time.Date(2026, 11, 9, h, m, 0, 0, time.Local))
	}
	if !at(3, 0) || !at(5, 59) {
		t.Fatal("should be open inside window")
	}
	if at(2, 59) || at(6, 0) {
		t.Fatal("should be closed outside window")
	}

	// 跨午夜 22:00-02:00
	c = WarmupConfig{StartMin: 22 * 60, EndMin: 2 * 60}
	if !at(23, 0) || !at(1, 0) {
		t.Fatal("overnight window should be open")
	}
	if at(12, 0) {
		t.Fatal("overnight window should be closed at noon")
	}
}

func TestChaptersToSegments(t *testing.T) {
	chapters := []ffChapter{
		{StartTime: "0", EndTime: "14.5", Tags: struct {
			Title string `json:"title"`
		}{Title: "Scene 1"}},
		{StartTime: "14.5", EndTime: "111.5", Tags: struct {
			Title string `json:"title"`
		}{Title: "Recap"}},
		{StartTime: "439.5", EndTime: "451.5", Tags: struct {
			Title string `json:"title"`
		}{Title: "Intro"}},
		{StartTime: "451.5", EndTime: "2161.45", Tags: struct {
			Title string `json:"title"`
		}{Title: "Scene 3"}},
		{StartTime: "2161.45", EndTime: "2371.9", Tags: struct {
			Title string `json:"title"`
		}{Title: "Credits"}},
		{StartTime: "2400", EndTime: "2402", Tags: struct {
			Title string `json:"title"`
		}{Title: "Opening Credits"}},
	}
	segs := chaptersToSegments("ep1", chapters)
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3: %+v", len(segs), segs)
	}
	if segs[0].Type != store.SegmentRecap || segs[1].Type != store.SegmentIntro || segs[2].Type != store.SegmentOutro {
		t.Fatalf("unexpected types: %+v", segs)
	}
	// 12 秒的 title card 必须保留；IntroSkipper 的 15 秒下限会把它丢掉。
	if got := segs[1].EndTicks - segs[1].StartTicks; got != 12*10_000_000 {
		t.Fatalf("intro duration ticks = %d", got)
	}
	// 2 秒的 Opening Credits 低于最短时长，应被过滤。
	for _, s := range segs {
		if s.StartTicks == 2400*10_000_000 {
			t.Fatalf("too-short chapter kept: %+v", s)
		}
	}
}

func TestClassifyChapterGenericIgnored(t *testing.T) {
	for _, name := range []string{"Chapter 01", "Scene 2", "Part 01", "第 01 章"} {
		if _, ok := classifyChapter(name); ok {
			t.Fatalf("%q should be ignored", name)
		}
	}
}

func TestClassifyChapterNumberedOPED(t *testing.T) {
	cases := map[string]string{
		"OP2":      store.SegmentIntro,
		"OP 2":     store.SegmentIntro,
		"NCOP":     store.SegmentIntro,
		"オープニング":   store.SegmentIntro,
		"ED2":      store.SegmentOutro,
		"ED 2":     store.SegmentOutro,
		"NCED":     store.SegmentOutro,
		"Ending 2": store.SegmentOutro,
		"エンディング":   store.SegmentOutro,
	}
	for name, want := range cases {
		got, ok := classifyChapter(name)
		if !ok || got != want {
			t.Fatalf("classifyChapter(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

// 季中换 OP 后只有两集共享新片段：单伙伴但足够长、相关度高也要收。
func TestConsensusSingleLongPartner(t *testing.T) {
	cands := []fpCandidate{{partner: 1, start: 44, end: 140, score: 0.91}}
	if span, ok := consensus(cands, 0, 1420, 15, 600, 0.5, false); !ok {
		t.Fatal("single long partner should be accepted")
	} else if span.start != 44 || span.end != 140 {
		t.Fatalf("span = %.0f-%.0f", span.start, span.end)
	}

	short := []fpCandidate{{partner: 1, start: 44, end: 70, score: 0.91}}
	if _, ok := consensus(short, 0, 1420, 15, 600, 0.5, false); ok {
		t.Fatal("short single partner should be rejected")
	}

	weak := []fpCandidate{{partner: 1, start: 44, end: 140, score: 0.72}}
	if _, ok := consensus(weak, 0, 1420, 15, 600, 0.5, false); ok {
		t.Fatal("weak single partner should be rejected")
	}
}

func TestFindRepeatSynthetic(t *testing.T) {
	// 600 个 0.5s 帧；把 a 的 100..300 帧复制到 b 的 200..400。
	base := make([]float64, 600)
	rng := rand.New(rand.NewSource(1))
	for i := range base {
		base[i] = rng.NormFloat64()
	}
	a := make([]float64, 600)
	copy(a, base)
	b := make([]float64, 600)
	copy(b[200:], base[100:300])

	sa, sb, _, ok := findRepeat(a, b)
	if !ok {
		t.Fatal("repeat not found")
	}
	// 8 秒相关窗会让边界提前几秒，允许 ±8 秒误差。
	if math.Abs(sa.start-50) > 8 || math.Abs(sa.end-150) > 8 {
		t.Fatalf("a span = %.1f-%.1f, want 50-150", sa.start, sa.end)
	}
	if math.Abs(sb.start-100) > 8 || math.Abs(sb.end-200) > 8 {
		t.Fatalf("b span = %.1f-%.1f, want 100-200", sb.start, sb.end)
	}
}

// 两集片尾经常差 0.5 秒（整数帧不对齐），粗 lag 步长会跳过正确偏移。
func TestFindRepeatHalfSecondOffset(t *testing.T) {
	base := make([]float64, 600)
	rng := rand.New(rand.NewSource(2))
	for i := range base {
		base[i] = rng.NormFloat64()
	}
	a := make([]float64, 600)
	copy(a, base)
	b := make([]float64, 600)
	copy(b[201:], base[100:300])

	sa, sb, _, ok := findRepeat(a, b)
	if !ok {
		t.Fatal("repeat with 0.5s offset not found")
	}
	if math.Abs(sa.start-50) > 8 || math.Abs(sb.start-100.5) > 8 {
		t.Fatalf("spans = %.1f / %.1f, want ~50 / ~100.5", sa.start, sb.start)
	}
}

// 新入库条目要立即进入探测队列；已探测过的片源只补片段。
func TestWarmIngestedItemsQueuesSources(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "lime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	lib, err := db.UpsertLibrary("t", "/t", "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&store.Item{
		ID: "m-new", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "新片",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&store.Item{
		ID: "m-done", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie, Name: "老片",
	}); err != nil {
		t.Fatal(err)
	}
	for _, src := range []*store.MediaSource{
		{ID: "s-new", ItemID: "m-new", URL: "http://x/new", ProbeStatus: "none"},
		{ID: "s-done", ItemID: "m-done", URL: "http://x/done", ProbeStatus: "done"},
	} {
		if err := db.UpsertMediaSource(src); err != nil {
			t.Fatal(err)
		}
	}

	p := &Prober{
		db:       db,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		enabled:  true,
		queue:    make(chan probeJob, 8),
		inflight: make(map[string]chan struct{}),
	}
	p.segmentsOn.Store(true)

	if n := p.WarmIngestedItems([]string{"m-new", "m-done"}); n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		j := <-p.queue
		got[j.sourceID+"/"+strconv.FormatBool(j.segmentsOnly)] = true
	}
	if !got["s-new/false"] || !got["s-done/true"] {
		t.Fatalf("jobs = %v, want s-new probe + s-done segments", got)
	}
}

func TestPickFingerprintSources(t *testing.T) {
	srcs := make([]store.SeasonEpisodeSource, 20)
	for i := range srcs {
		srcs[i] = store.SeasonEpisodeSource{ItemID: "e" + strconv.Itoa(i+1), IndexNumber: i + 1}
	}
	ids := func(in []store.SeasonEpisodeSource) []string {
		out := make([]string, len(in))
		for i := range in {
			out[i] = in[i].ItemID
		}
		return out
	}

	got := pickFingerprintSources(srcs, []string{"e15"}, 8)
	if len(got) != 8 {
		t.Fatalf("focus sample = %d, want 8", len(got))
	}
	found := false
	for _, s := range got {
		if s.ItemID == "e15" {
			found = true
		}
	}
	if !found {
		t.Fatalf("focus episode missing: %v", ids(got))
	}

	got = pickFingerprintSources(srcs, []string{"e2", "e18"}, 8)
	if len(got) != 8 {
		t.Fatalf("two-focus sample = %d, want 8", len(got))
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.ItemID] = true
	}
	if !seen["e2"] || !seen["e18"] {
		t.Fatalf("focus episodes missing: %v", ids(got))
	}
}
