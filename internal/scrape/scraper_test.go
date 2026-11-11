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
