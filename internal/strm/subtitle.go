package strm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Subtitle 是 STRM 同目录下的外挂字幕。
type Subtitle struct {
	Path     string
	Format   string // srt / ass / ssa / vtt
	Title    string
	Language string // ISO 639-2：chi / eng / jpn / kor
}

var subtitleExts = map[string]bool{
	".srt": true,
	".ass": true,
	".ssa": true,
	".vtt": true,
}

// FindSubtitles 查找与 STRM 同名的外挂字幕。
// 支持：
//
//	movie.strm + movie.ass
//	movie.strm + movie.chs.srt
//	movie.strm + movie.zh-CN.vtt
func FindSubtitles(strmPath string) []Subtitle {
	if strmPath == "" {
		return nil
	}
	dir := filepath.Dir(strmPath)
	base := filepath.Base(strmPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	out := make([]Subtitle, 0, 2)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !subtitleExts[ext] {
			continue
		}
		nameStem := strings.TrimSuffix(name, filepath.Ext(name))
		if nameStem != stem && !strings.HasPrefix(nameStem, stem+".") {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(nameStem, stem), ".")
		lang, title := subtitleMeta(suffix, stem)
		out = append(out, Subtitle{
			Path:     filepath.Join(dir, name),
			Format:   strings.TrimPrefix(ext, "."),
			Title:    title,
			Language: lang,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func subtitleMeta(suffix, stem string) (lang, title string) {
	low := strings.ToLower(suffix)
	switch {
	case containsAny(low, "chs", "zh-cn", "zh_cn", "sc", "简体", "简中"):
		lang = "chi"
	case containsAny(low, "cht", "zh-tw", "zh_tw", "tc", "繁体", "繁中"):
		lang = "chi"
	case containsAny(low, "eng", "english", "en"):
		lang = "eng"
	case containsAny(low, "jpn", "japanese", "ja", "日文", "日语"):
		lang = "jpn"
	case containsAny(low, "kor", "korean", "ko", "韩文", "韩语"):
		lang = "kor"
	}

	// 没有语言后缀时，从完整文件名猜一下
	if lang == "" {
		lowStem := strings.ToLower(stem)
		switch {
		case containsAny(lowStem, "中文字幕", "中文", "chs", "cht", "简体", "繁体"):
			lang = "chi"
		case containsAny(lowStem, "english", "eng"):
			lang = "eng"
		case containsAny(lowStem, "日本語", "日语", "jpn"):
			lang = "jpn"
		}
	}

	title = suffix
	if title == "" {
		switch lang {
		case "chi":
			title = "中文字幕"
		case "eng":
			title = "English"
		case "jpn":
			title = "日本語"
		case "kor":
			title = "한국어"
		default:
			title = "字幕"
		}
	}
	return lang, title
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
