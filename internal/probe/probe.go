// Package probe 用 ffprobe 远程探测片源的完整媒体信息（视频/音频/字幕轨道）。
// 只探测 PanSave 的反代地址，结果落库缓存；每个片源只探一次。
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lime/internal/config"
	"lime/internal/store"
)

type Prober struct {
	enabled bool
	bin     string
	db      *store.DB
	log     *slog.Logger
	timeout time.Duration
	queue   chan string

	mu       sync.Mutex
	inflight map[string]chan struct{}

	warmupMu  sync.Mutex
	warmupCfg WarmupConfig
	warmupOn  bool
}

func New(cfg config.Config, db *store.DB, log *slog.Logger) *Prober {
	p := &Prober{
		db:       db,
		log:      log,
		timeout:  cfg.ProbeTimeout,
		queue:    make(chan string, 256),
		inflight: make(map[string]chan struct{}),
	}
	if !cfg.ProbeEnabled {
		return p
	}
	if !ffprobeAvailable() {
		log.Warn("ffprobe not found; media probing disabled")
		return p
	}
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return p
	}
	p.enabled = true
	p.bin = bin

	workers := cfg.ProbeWorkers
	if workers <= 0 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	log.Info("media probe enabled", "ffprobe", bin, "workers", workers, "timeout", cfg.ProbeTimeout.String())
	return p
}

func ffprobeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// Enabled 表示 ffprobe 可用且配置打开了探测。
func (p *Prober) Enabled() bool { return p != nil && p.enabled }

// Enqueue 把片源加入探测队列，返回一个完成信号（已在探测中则返回现有的）。
func (p *Prober) Enqueue(src *store.MediaSource) <-chan struct{} {
	if p == nil || !p.enabled || src == nil {
		return nil
	}
	// done 缓存永久有效；failed 隔 24 小时再试，避免反复打不通的源。
	if src.ProbeStatus == "done" {
		return nil
	}
	if src.ProbeStatus == "failed" && time.Since(src.UpdatedAt) < 24*time.Hour {
		return nil
	}

	p.mu.Lock()
	if ch, ok := p.inflight[src.ID]; ok {
		p.mu.Unlock()
		return ch
	}
	ch := make(chan struct{})
	p.inflight[src.ID] = ch
	p.mu.Unlock()

	select {
	case p.queue <- src.ID:
		return ch
	default:
		p.mu.Lock()
		delete(p.inflight, src.ID)
		p.mu.Unlock()
		close(ch)
		p.log.Warn("probe queue full, skipped", "source", src.ID)
		return nil
	}
}

// WaitAll 最多等 d，等所有探测完成或超时。d<=0 立即返回。
func WaitAll(chans []<-chan struct{}, d time.Duration) {
	if len(chans) == 0 || d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	for _, ch := range chans {
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

// WarmupConfig 控制后台预热探测的时机和范围。
type WarmupConfig struct {
	Enabled bool
	// Scope: favorites（收藏的电影/剧集单集）/ recent（最近 7 天+进度）/ all（全库）
	Scope string
	// StartMin/EndMin 是每天允许运行的时间窗（本地时区，-1 表示不限）。
	StartMin int
	EndMin   int
	// MaxRun 是一次激活后最多连续跑多久，0 表示到窗口结束。
	MaxRun time.Duration
	// Batch 是每批送入队列的片源数量。
	Batch int
}

// ParseWindow 解析 "03:00-06:00" 形式的时间窗，返回当天的分钟数。
// 空字符串或格式不对返回 ok=false（表示不限时间）。
func ParseWindow(v string) (start, end int, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(v, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	parse := func(s string) (int, bool) {
		s = strings.TrimSpace(s)
		hm := strings.SplitN(s, ":", 2)
		if len(hm) != 2 {
			return 0, false
		}
		h, err1 := strconv.Atoi(hm[0])
		m, err2 := strconv.Atoi(hm[1])
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, false
		}
		return h*60 + m, true
	}
	a, ok1 := parse(parts[0])
	b, ok2 := parse(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return a, b, true
}

func (c WarmupConfig) open(t time.Time) bool {
	if c.StartMin < 0 || c.EndMin < 0 || c.StartMin == c.EndMin {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if c.StartMin < c.EndMin {
		return m >= c.StartMin && m < c.EndMin
	}
	return m >= c.StartMin || m < c.EndMin // 跨午夜
}

// StartWarmup 设置预热参数并启动后台调度（调度协程只启动一次）。
// 之后可以通过 SetWarmupConfig 在运行中修改范围/时间窗，无需重启。
func (p *Prober) StartWarmup(cfg WarmupConfig) {
	if p == nil || !p.enabled {
		return
	}
	p.SetWarmupConfig(cfg)

	p.warmupMu.Lock()
	already := p.warmupOn
	p.warmupOn = true
	p.warmupMu.Unlock()
	if already {
		return
	}
	go p.runWarmup()
}

// SetWarmupConfig 运行中更新预热参数，下一轮调度生效。
func (p *Prober) SetWarmupConfig(cfg WarmupConfig) {
	if p == nil || !p.enabled {
		return
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 50
	}
	if cfg.MaxRun < 0 {
		cfg.MaxRun = 0
	}
	p.warmupMu.Lock()
	p.warmupCfg = cfg
	p.warmupMu.Unlock()

	window := "all-day"
	if cfg.StartMin >= 0 && cfg.EndMin >= 0 && cfg.StartMin != cfg.EndMin {
		window = fmt.Sprintf("%02d:%02d-%02d:%02d",
			cfg.StartMin/60, cfg.StartMin%60, cfg.EndMin/60, cfg.EndMin%60)
	}
	p.log.Info("probe warmup scheduled",
		"enabled", cfg.Enabled, "scope", cfg.Scope, "window", window,
		"max_run", cfg.MaxRun.String(), "batch", cfg.Batch)
}

func (p *Prober) warmupConfig() WarmupConfig {
	p.warmupMu.Lock()
	defer p.warmupMu.Unlock()
	return p.warmupCfg
}

// runWarmup 是常驻调度：只在时间窗内跑，每次激活最多跑 MaxRun。
func (p *Prober) runWarmup() {
	// 启动后先让扫描/刮削忙完，再开始预热。
	time.Sleep(2 * time.Minute)
	for {
		cfg := p.warmupConfig()
		if !cfg.Enabled || !cfg.open(time.Now()) {
			time.Sleep(time.Minute)
			continue
		}

		var deadline time.Time
		if cfg.MaxRun > 0 {
			deadline = time.Now().Add(cfg.MaxRun)
		}
		for {
			cfg = p.warmupConfig() // 面板改了参数立即生效
			if !cfg.Enabled || !cfg.open(time.Now()) {
				break
			}
			if !deadline.IsZero() && time.Now().After(deadline) {
				p.log.Info("probe warmup run limit reached", "max_run", cfg.MaxRun.String())
				break
			}
			if n := p.warmupOnce(cfg.Batch, cfg.Scope); n == 0 {
				break
			}
			time.Sleep(5 * time.Second)
		}
		time.Sleep(time.Minute)
	}
}

// WarmSeries 立即把某部剧里未探测的单集加入队列。
// 播放开始/进度上报时调用，最多一次 30 集，避免长剧把队列打满；其余靠夜间预热。
func (p *Prober) WarmSeries(seriesID string, limit int) {
	if p == nil || !p.enabled || seriesID == "" {
		return
	}
	go func() {
		srcs, err := p.db.SourcesForSeriesWarm(seriesID, limit)
		if err != nil {
			p.log.Warn("series warm query failed", "series", seriesID, "err", err)
			return
		}
		if len(srcs) == 0 {
			return
		}
		n := 0
		for _, s := range srcs {
			if p.Enqueue(s) != nil {
				n++
			}
		}
		p.log.Info("series warm queued", "series", seriesID, "sources", len(srcs), "queued", n)
	}()
}

func (p *Prober) warmupOnce(limit int, scope string) int {
	since := int64(0)
	mode := strings.ToLower(scope)
	switch mode {
	case "all":
	case "recent":
		since = time.Now().Add(-7 * 24 * time.Hour).Unix()
	case "watching":
	default:
		mode = "favorites"
	}

	srcs, err := p.db.SourcesForProbeWarmup(since, limit, mode)
	if err != nil {
		p.log.Warn("probe warmup query failed", "err", err)
		return 0
	}
	if len(srcs) == 0 {
		return 0
	}

	chans := make([]<-chan struct{}, 0, len(srcs))
	for _, s := range srcs {
		if ch := p.Enqueue(s); ch != nil {
			chans = append(chans, ch)
		}
	}
	// 单批最多等 10 分钟，超时的继续在 worker 里跑。
	WaitAll(chans, 10*time.Minute)
	p.log.Info("probe warmup batch", "scope", scope, "queued", len(chans))
	return len(chans)
}

func (p *Prober) worker() {
	for id := range p.queue {
		p.probeOne(id)
		p.mu.Lock()
		if ch, ok := p.inflight[id]; ok {
			close(ch)
			delete(p.inflight, id)
		}
		p.mu.Unlock()
	}
}

func (p *Prober) probeOne(id string) {
	src, err := p.db.GetMediaSource(id)
	if err != nil {
		p.log.Warn("probe load source failed", "source", id, "err", err)
		return
	}
	if src.ProbeStatus == "done" || strings.TrimSpace(src.URL) == "" {
		return
	}

	res, err := p.run(src.URL)
	if err != nil {
		_ = p.db.SetProbeResult(id, "failed", 0)
		p.log.Warn("probe failed", "item", src.ItemID, "err", err)
		return
	}

	streams := convert(res)
	if len(streams) == 0 {
		_ = p.db.SetProbeResult(id, "failed", 0)
		p.log.Warn("probe returned no streams", "item", src.ItemID)
		return
	}
	if err := p.db.ReplaceMediaStreams(id, streams); err != nil {
		_ = p.db.SetProbeResult(id, "failed", 0)
		p.log.Warn("save probed streams failed", "item", src.ItemID, "err", err)
		return
	}

	ticks := secondsToTicks(parseFloat(res.Format.Duration))
	if err := p.db.SetProbeResult(id, "done", ticks); err != nil {
		p.log.Warn("save probe status failed", "item", src.ItemID, "err", err)
		return
	}
	// 把真实时长回填到条目：客户端靠 items.run_time_ticks 画进度条，
	// 缺了它 /Items/Resume 里的条目看起来就像「没有播放记录」。
	if ticks > 0 {
		if err := p.db.FillItemRuntimeFromSources(src.ItemID); err != nil {
			p.log.Warn("fill item runtime failed", "item", src.ItemID, "err", err)
		}
	}
	// 日志里不要出现 URL（可能带鉴权参数）。
	p.log.Info("media probed", "item", src.ItemID, "streams", len(streams),
		"duration_sec", int(ticks/10_000_000))
}

type ffprobeResult struct {
	Streams []ffStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

type ffStream struct {
	Index              int               `json:"index"`
	CodecName          string            `json:"codec_name"`
	CodecType          string            `json:"codec_type"`
	Width              int               `json:"width"`
	Height             int               `json:"height"`
	BitRate            string            `json:"bit_rate"`
	Profile            string            `json:"profile"`
	Level              int               `json:"level"`
	PixFmt             string            `json:"pix_fmt"`
	BitsPerRawSample   string            `json:"bits_per_raw_sample"`
	AvgFrameRate       string            `json:"avg_frame_rate"`
	RFrameRate         string            `json:"r_frame_rate"`
	ColorPrimaries     string            `json:"color_primaries"`
	ColorTransfer      string            `json:"color_transfer"`
	ColorSpace         string            `json:"color_space"`
	FieldOrder         string            `json:"field_order"`
	DisplayAspectRatio string            `json:"display_aspect_ratio"`
	SampleRate         string            `json:"sample_rate"`
	Channels           int               `json:"channels"`
	ChannelLayout      string            `json:"channel_layout"`
	Tags               map[string]string `json:"tags"`
	Disposition        struct {
		Default         int `json:"default"`
		Forced          int `json:"forced"`
		HearingImpaired int `json:"hearing_impaired"`
	} `json:"disposition"`
	SideDataList []struct {
		SideDataType string `json:"side_data_type"`
	} `json:"side_data_list"`
}

func (p *Prober) run(rawURL string) (*ffprobeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.bin,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-probesize", "5000000",
		"-analyzeduration", "5000000",
		rawURL,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffprobe timeout after %s", p.timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("ffprobe: %s", firstLine(string(exitErr.Stderr)))
		}
		return nil, err
	}

	var res ffprobeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("decode ffprobe output: %w", err)
	}
	return &res, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func convert(res *ffprobeResult) []store.MediaStream {
	out := make([]store.MediaStream, 0, len(res.Streams))
	for _, s := range res.Streams {
		st := store.MediaStream{
			Index:         s.Index,
			Codec:         s.CodecName,
			Profile:       s.Profile,
			Language:      s.Tags["language"],
			Title:         s.Tags["title"],
			IsDefault:     s.Disposition.Default != 0,
			IsForced:      s.Disposition.Forced != 0,
			SampleRate:    parseInt(s.SampleRate),
			Channels:      s.Channels,
			ChannelLayout: s.ChannelLayout,
			BitRate:       parseInt64(s.BitRate),
		}
		if st.ChannelLayout == "" && st.Channels > 0 {
			st.ChannelLayout = channelLayout(st.Channels)
		}

		switch s.CodecType {
		case "video":
			st.Type = "Video"
			st.Width = s.Width
			st.Height = s.Height
			st.BitDepth = videoBitDepth(s)
			st.FrameRate = parseRate(s.AvgFrameRate)
			if st.FrameRate == 0 {
				st.FrameRate = parseRate(s.RFrameRate)
			}
			st.VideoRange = videoRange(s)
			st.AspectRatio = aspectRatio(s)
			st.IsInterlaced = isInterlaced(s.FieldOrder)
			st.ColorPrimaries = s.ColorPrimaries
			st.ColorTransfer = s.ColorTransfer
			st.ColorSpace = s.ColorSpace
			st.PixelFormat = s.PixFmt
			if s.Level > 0 {
				st.Level = float64(s.Level)
			}
		case "audio":
			st.Type = "Audio"
		case "subtitle":
			st.Type = "Subtitle"
		default:
			continue
		}
		out = append(out, st)
	}
	return out
}

func videoBitDepth(s ffStream) int {
	if n := parseInt(s.BitsPerRawSample); n > 0 {
		return n
	}
	// yuv420p10le / p010le / yuv420p12le
	if m := regexp.MustCompile(`p0?(10|12|16)`).FindStringSubmatch(s.PixFmt); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if s.PixFmt != "" {
		return 8
	}
	return 0
}

func videoRange(s ffStream) string {
	for _, sd := range s.SideDataList {
		name := strings.ToUpper(sd.SideDataType)
		if strings.Contains(name, "DOVI") || strings.Contains(name, "DOLBY VISION") {
			return "DoVi"
		}
	}
	switch strings.ToLower(s.ColorTransfer) {
	case "smpte2084":
		return "HDR10"
	case "arib-std-b67":
		return "HLG"
	}
	return "SDR"
}

func isInterlaced(fieldOrder string) bool {
	switch strings.ToLower(fieldOrder) {
	case "", "progressive", "unknown":
		return false
	}
	return true
}

func aspectRatio(s ffStream) string {
	if s.DisplayAspectRatio != "" && s.DisplayAspectRatio != "0:1" {
		return s.DisplayAspectRatio
	}
	if s.Width <= 0 || s.Height <= 0 {
		return ""
	}
	g := gcd(s.Width, s.Height)
	return fmt.Sprintf("%d:%d", s.Width/g, s.Height/g)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func channelLayout(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return ""
}

func parseRate(v string) float64 {
	if v == "" || v == "0/0" {
		return 0
	}
	if i := strings.IndexByte(v, '/'); i >= 0 {
		num, err1 := strconv.ParseFloat(v[:i], 64)
		den, err2 := strconv.ParseFloat(v[i+1:], 64)
		if err1 == nil && err2 == nil && den != 0 {
			return math.Round(num/den*1000) / 1000
		}
		return 0
	}
	f, _ := strconv.ParseFloat(v, 64)
	return math.Round(f*1000) / 1000
}

func parseFloat(v string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func parseInt64(v string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n
}

func secondsToTicks(sec float64) int64 {
	if sec <= 0 {
		return 0
	}
	return int64(sec * 10_000_000)
}
