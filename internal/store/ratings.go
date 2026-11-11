package store

import (
	"strconv"
	"strings"
)

// RatingScore 把 TMDB 返回的分级字符串映射成 1-5：
//
//	1 = G / TV-Y / TV-G
//	2 = PG / TV-PG
//	3 = PG-13 / TV-14 / 12
//	4 = R / 15 / 16
//	5 = NC-17 / TV-MA / 18+
//
// 0 表示未知或未分级。未分级条目在家长控制里默认放行，
// 否则刮削没跑完的库会对受限账号整库不可见。
func RatingScore(rating string) int {
	s := strings.ToUpper(strings.TrimSpace(rating))
	if s == "" {
		return 0
	}

	switch s {
	case "G", "TV-Y", "TV-Y7", "TV-Y7-FV", "TV-G", "U", "ALL", "全年龄", "普遍级", "0", "0+":
		return 1
	case "PG", "TV-PG", "6", "6+", "7", "7+":
		return 2
	case "PG-13", "TV-14", "12", "12+", "12A", "IIA", "IIB", "T", "辅导级", "保護級", "保护级":
		return 3
	case "R", "15", "16", "III", "M", "MA15+", "R15+":
		return 4
	case "NC-17", "TV-MA", "18", "18+", "X", "R18", "R18+", "18禁", "限制级", "成人级":
		return 5
	}

	// 兜底解析 "FSK 16"、"M/16" 这类带数字的地区分级。
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			continue
		}
		switch {
		case n >= 18:
			return 5
		case n >= 15:
			return 4
		case n >= 12:
			return 3
		case n >= 6:
			return 2
		default:
			return 1
		}
	}
	return 0
}
