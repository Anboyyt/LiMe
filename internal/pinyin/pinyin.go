// Package pinyin 把中文标题转成可排序、可搜索的拼音键。
// 只做排序和搜索用，不参与展示，也不影响条目 ID。
package pinyin

import (
	"strings"
	"unicode"

	gopinyin "github.com/mozillazg/go-pinyin"
)

var args = gopinyin.NewArgs()

// Key 返回全拼和首字母，均为小写、只保留字母和数字。
// 不含汉字时返回空串，调用方应继续沿用原来的排序名。
//
//	三体       -> "santi", "st"
//	流浪地球2  -> "liulangdiqiu2", "lldq2"
//	Spider-Man -> "", ""
func Key(s string) (full, abbr string) {
	if s == "" {
		return "", ""
	}
	var fb, ab strings.Builder
	hasHan := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			ps := gopinyin.SinglePinyin(r, args)
			if len(ps) == 0 || ps[0] == "" {
				continue
			}
			p := ps[0]
			fb.WriteString(p)
			ab.WriteString(p[:1])
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			lr := unicode.ToLower(r)
			fb.WriteRune(lr)
			ab.WriteRune(lr)
		}
	}
	if !hasHan {
		return "", ""
	}
	return fb.String(), ab.String()
}

// PrefixRange 返回前缀查询的上下界，配合 pinyin/pinyin_abbr 的 B-tree 索引
// 使用（SQLite 对普通 LIKE 前缀不保证走索引，范围查询可以）。
// 上界用最大码位补齐，保证 "sa" 覆盖 "santi" 但不包含 "sb"。
func PrefixRange(q string) (lo, hi string) {
	q = strings.ToLower(strings.TrimSpace(q))
	return q, q + "\U0010FFFF"
}
