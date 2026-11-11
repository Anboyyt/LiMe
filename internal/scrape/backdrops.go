package scrape

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lime/internal/store"
	"lime/internal/tmdb"
)

// BackfillStatus 是图片回填任务（背景图 / 海报）的进度。
type BackfillStatus struct {
	Running      bool   `json:"Running"`
	Processed    int    `json:"Processed"`
	Total        int    `json:"Total"`
	LastFinished int64  `json:"LastFinished"`
	LastError    string `json:"LastError"`
}

// BackdropStatus 保留旧名字，接口和面板都用它。
type BackdropStatus = BackfillStatus

// BackdropStatus 返回当前进度快照。
func (s *Scraper) BackdropStatus() BackdropStatus {
	s.backdropMu.Lock()
	defer s.backdropMu.Unlock()
	return s.backdrop
}

// StartBackdropBackfill 启动后台回填；已有任务（背景图或海报）在跑时返回 false。
func (s *Scraper) StartBackdropBackfill() bool {
	s.backdropMu.Lock()
	if s.backdrop.Running || s.poster.Running {
		s.backdropMu.Unlock()
		return false
	}
	s.backdrop = BackdropStatus{Running: true}
	s.backdropMu.Unlock()
	go s.runBackdropBackfill()
	return true
}

func (s *Scraper) runBackdropBackfill() {
	items, err := s.db.BackdropBackfillItems()
	if err != nil {
		s.finishBackdrop(err)
		return
	}
	s.backdropMu.Lock()
	s.backdrop.Total = len(items)
	s.backdropMu.Unlock()

	// 复用刮削的超时上限（默认 6h），避免代理挂掉时任务永远跑不完。
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	// 下载原图是纯 I/O；并发太高会让 TMDB 代理更容易超时。
	workers := s.cfg.BackdropWorkers
	if workers <= 0 {
		workers = 5
	}
	if workers > 16 {
		workers = 16
	}
	jobs := make(chan store.BackdropItem)
	var wg sync.WaitGroup
	var done, failed atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				if err := s.refreshBackdrop(ctx, it); err != nil {
					failed.Add(1)
					s.log.Warn("backdrop refresh failed",
						"item", it.ID, "tmdb", it.TmdbID, "err", err)
				}
				n := int(done.Add(1))
				s.backdropMu.Lock()
				s.backdrop.Processed = n
				s.backdropMu.Unlock()
			}
		}()
	}

loop:
	for _, it := range items {
		select {
		case jobs <- it:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()

	if n := int64(len(items)); n > 0 && failed.Load() == n {
		s.finishBackdrop(errors.New("全部条目下载失败，检查 TMDB Key / 代理"))
		return
	}
	s.finishBackdrop(nil)
}

func (s *Scraper) finishBackdrop(err error) {
	s.backdropMu.Lock()
	s.backdrop.Running = false
	s.backdrop.LastFinished = time.Now().Unix()
	if err != nil {
		s.backdrop.LastError = err.Error()
	}
	s.backdropMu.Unlock()
}

// refreshBackdrop 为一部电影/剧集挑分辨率最高的背景图，按 original 重新下载。
func (s *Scraper) refreshBackdrop(ctx context.Context, it store.BackdropItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var (
		set *tmdb.ImageSet
		err error
	)
	if it.Type == store.TypeMovie {
		set, err = s.client.MovieImageSet(ctx, it.TmdbID)
	} else {
		set, err = s.client.TVImageSet(ctx, it.TmdbID)
	}
	if err != nil {
		s.recordAPIError(it.ID, err)
		return err
	}
	_ = s.db.ClearScrapeFailure(it.ID, store.FailureNotFound, store.FailureAPI)
	s.pace()

	path := tmdb.PickBackdrop(set.Backdrops)
	if path == "" {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailureBackdrop)
		return nil
	}
	rel, err := s.client.DownloadImage(ctx, path, tmdb.BackdropSize, s.cfg.DataDir)
	if err != nil {
		_ = s.db.RecordScrapeFailure(it.ID, store.FailureBackdrop, err.Error())
		return err
	}
	_ = s.db.ClearScrapeFailure(it.ID, store.FailureBackdrop)
	if rel == "" || rel == it.BackdropPath {
		return nil
	}
	if err := s.db.ReplaceBackdrop(it.ID, it.BackdropPath, rel); err != nil {
		return err
	}
	s.removeOldBackdrop(it.BackdropPath)
	return nil
}

// removeOldBackdrop 删掉已经没有任何条目引用的旧图。路径只允许 images/ 下的相对路径。
func (s *Scraper) removeOldBackdrop(rel string) {
	if !strings.HasPrefix(rel, "images/") || strings.Contains(rel, "..") {
		return
	}
	n, err := s.db.CountBackdropRefs(rel)
	if err != nil || n > 0 {
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
		s.log.Warn("remove old backdrop failed", "path", rel, "err", err)
	}
}
