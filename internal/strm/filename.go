package strm

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Metadata 是从媒体文件名推断出的信息。
// 解析失败不是错误：字段留空即可，不影响播放。
type Metadata struct {
	Title         string
	Year          int
	Season        int
	Episode       int
	IsEpisode     bool
	Resolution    string
	Width         int
	Height        int
	VideoCodec    string
	AudioCodec    string
	AudioLang     string // ISO 639-2，如 chi/yue/jpn
	AudioLangName string // 展示名，如 国语/粤语
	Channels      int
	BitDepth      int
	FrameRate     float64
	VideoRange    string
	Source        string
	ReleaseGroup  string
	Container     string
	TmdbID        string
}

var (
	reSeasonEp  = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(?:S(\d{1,2})E(\d{1,4}))(?:[.\s_\-]|$)`)
	reSeasonEp2 = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(\d{1,2})x(\d{1,4})(?:[.\s_\-]|$)`)
	reChineseEp = regexp.MustCompile(`第(\d{1,4})[集话話]`)
	reYear      = regexp.MustCompile(`(?:^|[.\s_\-\(\[])((?:19|20)\d{2})(?:[.\s_\)\]]|$)`)
	reRes       = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(2160p|1080p|720p|480p|4k|8k)(?:[.\s_\-]|$)`)
	reCodec     = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(H\.?264|H\.?265|HEVC|x264|x265|AV1|VP9)(?:[.\s_\-]|$)`)
	// 顺序有讲究：DDP 在 DD 前，E-AC3 在 AC3 前，LPCM 在 PCM 前。
	// 前缀限制避免把标题里的字母误当编码。
	reAudio    = regexp.MustCompile(`(?i)(?:^|[.\s_\-\[\(])(DDP[\d.]*|DD\+[\d.]*|E-?AC3|AC3|DD[\d.]*|AAC[\d.]*|DTS(?:[.\s-]?(?:HD(?:[.\s-]?MA)?|X))?|TrueHD[\d.]*|FLAC|OPUS[\d.]*|MP3|LPCM|PCM)`)
	reBitDepth = regexp.MustCompile(`(?i)(\d{1,2})\s?bit`)
	reFPS      = regexp.MustCompile(`(?i)(\d{1,3}(?:\.\d+)?)\s?fps`)
	reRange    = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(SDR|HDR10\+|HDR10|HDR|DoVi|DV)(?:[.\s_\-]|$)`)
	reSource   = regexp.MustCompile(`(?i)(?:^|[.\s_\-])(WEB-?DL|WEBRip|BluRay|BDRip|HDTV|Remux|WEB)(?:[.\s_\-]|$)`)
	reGroup    = regexp.MustCompile(`-([A-Za-z0-9]+)$`)
)

// mediaExts 是可以安全当作容器扩展名去掉的后缀。
// 不在这个集合里的点分后缀（例如 S01E01）会被当成标题/季集信息保留。
var mediaExts = map[string]bool{
	"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true,
	"wmv": true, "flv": true, "webm": true, "ts": true, "m2ts": true,
	"mpg": true, "mpeg": true, "rmvb": true, "rm": true, "iso": true,
	"strm": true,
}

// Parse 解析文件名。输入可以是完整路径或纯文件名。
func Parse(name string) Metadata {
	var m Metadata

	base := filepath.Base(name)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
	// 只有真正像容器扩展名时才去掉最后一段。否则 "剧名.S01E01" 会把
	// S01E01 当成扩展名，季集识别直接失败。
	if !mediaExts[ext] {
		ext = ""
	}
	m.Container = ext

	stem := base
	if ext != "" {
		stem = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if stem == "" {
		stem = base
	}

	// 标题切点：优先 SxxExx，其次年份，最后技术标记。
	cut := len(stem)
	lower := func(i int) {
		if i >= 0 && i < cut {
			cut = i
		}
	}

	if loc := reSeasonEp.FindStringSubmatchIndex(stem); loc != nil {
		m.Season = atoiSafe(stem[loc[2]:loc[3]])
		m.Episode = atoiSafe(stem[loc[4]:loc[5]])
		m.IsEpisode = true
		lower(loc[0])
	} else if loc := reSeasonEp2.FindStringSubmatchIndex(stem); loc != nil {
		m.Season = atoiSafe(stem[loc[2]:loc[3]])
		m.Episode = atoiSafe(stem[loc[4]:loc[5]])
		m.IsEpisode = true
		lower(loc[0])
	} else if loc := reChineseEp.FindStringSubmatchIndex(stem); loc != nil {
		m.Episode = atoiSafe(stem[loc[2]:loc[3]])
		m.IsEpisode = true
		lower(loc[0])
	}

	if loc := reYear.FindStringSubmatchIndex(stem); loc != nil && loc[0] < cut {
		m.Year = atoiSafe(stem[loc[2]:loc[3]])
		lower(loc[0])
	}

	// 没有任何结构标记时，退到技术标记处截断。
	if cut == len(stem) {
		for _, re := range []*regexp.Regexp{reRes, reSource, reCodec} {
			if loc := re.FindStringIndex(stem); loc != nil {
				lower(loc[0])
			}
		}
	}

	title := cleanTitle(stem[:cut])
	if title == "" {
		title = cleanTitle(stem)
	}
	m.Title = title

	// ---- 技术字段：在整个 stem 上匹配 ----

	if loc := reRes.FindStringSubmatch(stem); loc != nil {
		m.Resolution = strings.ToLower(loc[1])
		m.Width, m.Height = resolutionSize(m.Resolution)
	}

	if loc := reCodec.FindStringSubmatch(stem); loc != nil {
		m.VideoCodec = normalizeCodec(loc[1])
	}

	if loc := reAudio.FindStringSubmatchIndex(stem); loc != nil {
		m.AudioCodec, m.Channels = normalizeAudio(stem[loc[2]:loc[3]])
		if m.Channels == 0 {
			// 声道可能写在下一个 token 里，例如 AC3.5.1
			tail := stem[loc[1]:min(len(stem), loc[1]+12)]
			m.Channels = parseChannels(tail)
		}
	}

	if loc := reBitDepth.FindStringSubmatch(stem); loc != nil {
		m.BitDepth = atoiSafe(loc[1])
	}

	if loc := reFPS.FindStringSubmatch(stem); loc != nil {
		m.FrameRate, _ = strconv.ParseFloat(loc[1], 64)
	}

	if loc := reRange.FindStringSubmatch(stem); loc != nil {
		m.VideoRange = normalizeRange(loc[1])
	}

	if loc := reSource.FindStringSubmatch(stem); loc != nil {
		m.Source = strings.ToUpper(loc[1])
	}

	if loc := reGroup.FindStringSubmatch(stem); loc != nil {
		m.ReleaseGroup = loc[1]
	}

	m.AudioLang, m.AudioLangName = detectAudioLang(stem)

	return m
}

// detectAudioLang 从文件名里的常见标记推断音轨语言。
// 只认明确的关键词，避免把普通英文单词误判成语言代码。
func detectAudioLang(s string) (string, string) {
	ls := strings.ToLower(s)
	cases := []struct {
		keys []string
		code string
		name string
	}{
		{[]string{"国配", "国语", "普通话", "mandarin"}, "chi", "国语"},
		{[]string{"粤语", "粤配", "cantonese"}, "yue", "粤语"},
		{[]string{"日语", "日配", "japanese"}, "jpn", "日语"},
		{[]string{"韩语", "韩配", "korean"}, "kor", "韩语"},
		{[]string{"英语", "英文", "english"}, "eng", "英语"},
	}
	for _, c := range cases {
		for _, k := range c.keys {
			if strings.Contains(ls, k) {
				return c.code, c.name
			}
		}
	}
	return "", ""
}

func atoiSafe(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func cleanTitle(s string) string {
	s = strings.NewReplacer("_", " ", ".", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimFunc(s, func(r rune) bool {
		return strings.ContainsRune(" [](){}《》·【】", r)
	})
}

func resolutionSize(res string) (int, int) {
	switch res {
	case "8k":
		return 7680, 4320
	case "2160p", "4k":
		return 3840, 2160
	case "1080p":
		return 1920, 1080
	case "720p":
		return 1280, 720
	case "480p":
		return 854, 480
	}
	return 0, 0
}

func normalizeCodec(raw string) string {
	switch strings.ToLower(strings.ReplaceAll(raw, ".", "")) {
	case "h264", "x264", "avc":
		return "h264"
	case "h265", "x265", "hevc":
		return "hevc"
	case "av1":
		return "av1"
	case "vp9":
		return "vp9"
	}
	return strings.ToLower(raw)
}

func normalizeAudio(raw string) (string, int) {
	upper := strings.ToUpper(raw)
	channels := parseChannels(upper)

	switch {
	case strings.HasPrefix(upper, "DDP"), strings.HasPrefix(upper, "DD+"),
		strings.HasPrefix(upper, "EAC3"), strings.HasPrefix(upper, "E-AC3"):
		return "eac3", channels
	case strings.HasPrefix(upper, "AC3"):
		return "ac3", channels
	case strings.HasPrefix(upper, "DD"):
		// 纯 DD 就是 Dolby Digital（AC3）
		return "ac3", channels
	case strings.HasPrefix(upper, "AAC"):
		return "aac", channels
	case strings.HasPrefix(upper, "DTS"):
		return "dts", channels
	case strings.HasPrefix(upper, "TRUEHD"):
		return "truehd", channels
	case strings.HasPrefix(upper, "FLAC"):
		return "flac", channels
	case strings.HasPrefix(upper, "OPUS"):
		return "opus", channels
	case strings.HasPrefix(upper, "MP3"):
		return "mp3", channels
	case strings.HasPrefix(upper, "LPCM"), strings.HasPrefix(upper, "PCM"):
		return "pcm", channels
	}
	return strings.ToLower(raw), channels
}

// parseChannels 从 "DDP5.1" 这类串里取声道数：5.1 -> 6，2.0 -> 2。
func parseChannels(s string) int {
	loc := regexp.MustCompile(`(\d)\.(\d)`).FindStringSubmatch(s)
	if loc == nil {
		return 0
	}
	front, _ := strconv.Atoi(loc[1])
	back, _ := strconv.Atoi(loc[2])
	if back > 0 {
		return front + 1
	}
	return front
}

func normalizeRange(raw string) string {
	switch strings.ToUpper(raw) {
	case "SDR":
		return "SDR"
	case "DOVI", "DV":
		// 保留杜比视界，版本列表要显示 DV
		return "DoVi"
	case "HDR":
		return "HDR"
	case "HDR10":
		return "HDR10"
	case "HDR10+":
		return "HDR10+"
	}
	return raw
}
