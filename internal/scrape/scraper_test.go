package scrape

import (
	"sync"
	"testing"
	"time"
)

// pace 是全局限流：多个 worker 并发调用时，两次请求之间仍至少间隔 paceInterval。
func TestPaceIsGlobal(t *testing.T) {
	s := &Scraper{paceInterval: 60 * time.Millisecond}
	const n = 5
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.pace()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	// 第一次不等待，后续 n-1 次各等约 60ms。
	if want := time.Duration(n-1) * 60 * time.Millisecond; elapsed < want*8/10 {
		t.Fatalf("elapsed = %v, want >= %v", elapsed, want*8/10)
	}
}

func TestYearFromDate(t *testing.T) {
	cases := map[string]int{
		"2020-12-11": 2020,
		"1999-01-01": 1999,
		"":           0,
		"20":         0,
		"abcd-01-01": 0,
		"1700-01-01": 0,
	}
	for in, want := range cases {
		if got := yearFromDate(in); got != want {
			t.Fatalf("yearFromDate(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNormalizePosterSize(t *testing.T) {
	for _, in := range []string{"w342", "w500", "w780", "original", "W780", " original "} {
		if _, ok := normalizePosterSize(in); !ok {
			t.Fatalf("normalizePosterSize(%q) rejected", in)
		}
	}
	for _, in := range []string{"", "w185", "huge", "../x"} {
		if _, ok := normalizePosterSize(in); ok {
			t.Fatalf("normalizePosterSize(%q) accepted", in)
		}
	}
	if v, ok := normalizePosterSize(" W780 "); !ok || v != "w780" {
		t.Fatalf("normalizePosterSize = %q, %v; want w780", v, ok)
	}
}

func TestNormalizeStillSize(t *testing.T) {
	for _, in := range []string{"w92", "w185", "w300", "original", "W300"} {
		if _, ok := normalizeStillSize(in); !ok {
			t.Fatalf("normalizeStillSize(%q) rejected", in)
		}
	}
	for _, in := range []string{"", "w500", "w780", "huge"} {
		if _, ok := normalizeStillSize(in); ok {
			t.Fatalf("normalizeStillSize(%q) accepted", in)
		}
	}
	if v, ok := normalizeStillSize(" W300 "); !ok || v != "w300" {
		t.Fatalf("normalizeStillSize = %q, %v; want w300", v, ok)
	}
}
