package store

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// PlayWindow 是用户允许播放的时段。空值表示不限制。
// 按本地时区判断（容器 TZ=Asia/Shanghai）。
type PlayWindow struct {
	Start string `json:"Start"` // HH:MM
	End   string `json:"End"`   // HH:MM
	// Days 是星期几，1=周一 … 7=周日。空表示每天。
	Days []int `json:"Days,omitempty"`
}

func (w PlayWindow) IsZero() bool {
	return strings.TrimSpace(w.Start) == "" && strings.TrimSpace(w.End) == ""
}

// ParsePlayWindow 解析数据库里的 JSON。解析失败返回空窗口（不限制），
// 避免一条坏数据把用户挡在外面。
func ParsePlayWindow(raw string) PlayWindow {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlayWindow{}
	}
	var w PlayWindow
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return PlayWindow{}
	}
	return w
}

// Marshal 序列化为存库的字符串，空窗口存空串。
func (w PlayWindow) Marshal() string {
	if w.IsZero() {
		return ""
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Validate 校验用户提交的时段格式。
func (w PlayWindow) Validate() error {
	if w.IsZero() {
		return nil
	}
	if _, ok := parseClock(w.Start); !ok {
		return errors.New("开始时间格式应为 HH:MM")
	}
	if _, ok := parseClock(w.End); !ok {
		return errors.New("结束时间格式应为 HH:MM")
	}
	for _, d := range w.Days {
		if d < 1 || d > 7 {
			return errors.New("星期取值应为 1-7")
		}
	}
	return nil
}

// Allows 判断 t 是否落在允许时段内。任何解析不了的情况都放行。
func (w PlayWindow) Allows(t time.Time) bool {
	if w.IsZero() {
		return true
	}
	start, ok1 := parseClock(w.Start)
	end, ok2 := parseClock(w.End)
	if !ok1 || !ok2 {
		return true
	}

	if len(w.Days) > 0 {
		day := int(t.Weekday())
		if day == 0 {
			day = 7
		}
		found := false
		for _, d := range w.Days {
			if d == day {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	cur := t.Hour()*60 + t.Minute()
	s := start[0]*60 + start[1]
	e := end[0]*60 + end[1]
	if s == e {
		return true
	}
	if s < e {
		return cur >= s && cur < e
	}
	// 跨夜：22:00-02:00
	return cur >= s || cur < e
}

// parseClock 解析 "HH:MM"，返回 [时, 分]。
func parseClock(s string) ([2]int, bool) {
	var out [2]int
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return out, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return out, false
	}
	out[0], out[1] = h, m
	return out, true
}
