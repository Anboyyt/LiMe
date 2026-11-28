package store

import (
	"testing"
	"time"
)

func at(hour, min int) time.Time {
	// 2026-11-30 是周一
	return time.Date(2026, 11, 30, hour, min, 0, 0, time.Local)
}

func TestPlayWindowAllows(t *testing.T) {
	allDay := PlayWindow{}
	if !allDay.Allows(at(3, 0)) {
		t.Fatal("empty window should allow everything")
	}

	day := PlayWindow{Start: "18:00", End: "23:00"}
	cases := []struct {
		t    time.Time
		want bool
	}{
		{at(17, 59), false},
		{at(18, 0), true},
		{at(22, 59), true},
		{at(23, 0), false},
	}
	for _, c := range cases {
		if got := day.Allows(c.t); got != c.want {
			t.Errorf("%s: got %v, want %v", c.t.Format("15:04"), got, c.want)
		}
	}

	// 跨夜 22:00-02:00
	night := PlayWindow{Start: "22:00", End: "02:00"}
	if !night.Allows(at(23, 30)) || !night.Allows(at(1, 0)) {
		t.Error("overnight window should allow late night")
	}
	if night.Allows(at(12, 0)) {
		t.Error("overnight window should reject noon")
	}

	// 只允许周一到周五
	weekday := PlayWindow{Start: "00:00", End: "23:59", Days: []int{1, 2, 3, 4, 5}}
	if !weekday.Allows(at(12, 0)) {
		t.Error("monday should be allowed")
	}
	sunday := time.Date(2026, 12, 6, 12, 0, 0, 0, time.Local)
	if weekday.Allows(sunday) {
		t.Error("sunday should be rejected")
	}
}

func TestPlayWindowParseFallback(t *testing.T) {
	if !ParsePlayWindow("{bad json").IsZero() {
		t.Fatal("bad json should fall back to no limit")
	}
	// 格式错误的时段按放行处理，避免一条坏数据把人锁在外面。
	w := PlayWindow{Start: "25:00", End: "26:00"}
	if !w.Allows(at(12, 0)) {
		t.Fatal("invalid clock should allow")
	}
}

func TestPlayWindowValidate(t *testing.T) {
	if err := (PlayWindow{}).Validate(); err != nil {
		t.Fatalf("empty window: %v", err)
	}
	if err := (PlayWindow{Start: "18:00", End: "23:00"}).Validate(); err != nil {
		t.Fatalf("valid window: %v", err)
	}
	if err := (PlayWindow{Start: "18:00"}).Validate(); err == nil {
		t.Fatal("missing end should fail")
	}
	if err := (PlayWindow{Start: "18:00", End: "23:00", Days: []int{0}}).Validate(); err == nil {
		t.Fatal("day 0 should fail")
	}
}

func TestPlayWindowMarshalRoundTrip(t *testing.T) {
	w := PlayWindow{Start: "18:00", End: "23:00", Days: []int{1, 7}}
	raw := w.Marshal()
	got := ParsePlayWindow(raw)
	if got.Start != w.Start || got.End != w.End || len(got.Days) != 2 {
		t.Fatalf("round trip = %+v", got)
	}
	if (PlayWindow{}).Marshal() != "" {
		t.Fatal("empty window should marshal to empty string")
	}
}
