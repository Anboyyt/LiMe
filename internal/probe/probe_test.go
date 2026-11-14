package probe

import (
	"encoding/json"
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
