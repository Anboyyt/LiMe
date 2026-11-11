package strm

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reTmdbInPath   = regexp.MustCompile(`\{tmdb-(\d+)\}`)
	reFolderParent = regexp.MustCompile(`^(.*?)\s*\((\d{4})\)\s*(?:\{tmdb-\d+\})?\s*$`)
	reSeasonFolder = regexp.MustCompile(`(?i)^season\s*(\d+)`)
)

// ParsePath 从相对路径的目录名推断标题、年份、TMDB ID、季号。
// 目录命名形如：逐爱天堂 Angel (2007) {tmdb-11801}/... - 1080p .../file.strm
// 返回 ok=false 表示目录结构不符合预期，调用方应退回文件名解析。
func ParsePath(relPath string) (Metadata, bool) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) < 2 {
		return Metadata{}, false
	}

	var m Metadata
	found := false

	// 目录部分（最后一段是文件名）
	for _, part := range parts[:len(parts)-1] {
		if !found {
			if loc := reTmdbInPath.FindStringSubmatch(part); loc != nil {
				m.TmdbID = loc[1]
			}
			if loc := reFolderParent.FindStringSubmatch(part); loc != nil {
				if title := strings.TrimSpace(loc[1]); title != "" {
					m.Title = title
					m.Year = atoiSafe(loc[2])
					if m.TmdbID == "" {
						if g := reTmdbInPath.FindStringSubmatch(part); g != nil {
							m.TmdbID = g[1]
						}
					}
					found = true
				}
			}
		}
		if m.Season == 0 {
			if loc := reSeasonFolder.FindStringSubmatch(part); loc != nil {
				m.Season = atoiSafe(loc[1])
			}
		}
	}

	if !found && m.Season == 0 {
		return m, m.TmdbID != ""
	}
	return m, found || m.Season > 0
}

// TmdbID 从任意字符串中提取 {tmdb-xxx}。
func TmdbID(s string) string {
	if loc := reTmdbInPath.FindStringSubmatch(s); loc != nil {
		return loc[1]
	}
	return ""
}
