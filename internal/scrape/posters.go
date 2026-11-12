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

// PosterStatus 返回当前海报回填进度。
func (s *Scraper) PosterStatus() BackfillStatus {
	s.backdropMu.Lock()
	defer s.backdropMu.Unlock()
	return s.poster
}

// StartPosterBackfill 启动后台回填；已有任务（背景图或海报）在跑时返回 false。
func (s *Scraper) StartPosterBackfill() bool {
	s.backdropMu.Lock()
	if s.backdrop.Running || s.poster.Running {
		s.backdropMu.Unlock()
		return false
	}
	s.poster = BackfillStatus{Running: true}
	s.backdropMu.Unlock()
	go s.runPosterBackfill()
	return true
}

func (s *Scraper) runPosterBackfill() {
	items, err := s.db.PosterBackfillItems()
	if err != nil {
		s.finishPoster(err)
		return
	}
	s.backdropMu.Lock()
	s.poster.Total = len(items)
	s.backdropMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	workers := s.cfg.BackdropWorkers
	if workers <= 0 {
		workers = 5
	}
	if workers > 16 {
		workers = 16
	}
	jobs := make(chan store.PosterItem)
	var wg sync.WaitGroup
	var done, failed atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				if err := s.refreshPoster(ctx, it); err != nil {
					failed.Add(1)
					s.log.Warn("poster refresh failed",
						"item", it.ID, "tmdb", it.TmdbID, "err", err)
				}
				n := int(done.Add(1))
				s.backdropMu.Lock()
				s.poster.Processed = n
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
		s.finishPoster(errors.New("全部条目下载失败，检查 TMDB Key / 代理"))
		return
	}
	s.finishPoster(nil)
}

func (s *Scraper) finishPoster(err error) {
	s.backdropMu.Lock()
	s.poster.Running = false
	s.poster.LastFinished = time.Now().Unix()
	if err != nil {
		s.poster.LastError = err.Error()
	}
	s.backdropMu.Unlock()
}

// refreshPoster 重新取该条目当前语言的海报路径，按 w780 下载并替换旧图。
// 用详情接口的 poster_path（而不是图片列表里评分最高的那张），保证换的只是
// 分辨率，海报本身和当初刮削时是同一张。
func (s *Scraper) refreshPoster(ctx context.Context, it store.PosterItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var (
		path string
		err  error
	)
	if it.Type == store.TypeMovie {
		path, err = s.client.MoviePoster(ctx, it.TmdbID)
	} else {
		path, err = s.client.TVPoster(ctx, it.TmdbID)
	}
	if err != nil {
		s.recordAPIError(it.ID, err)
		return err
	}
	_ = s.db.ClearScrapeFailure(it.ID, store.FailureNotFound, store.FailureAPI)
	s.pace()
	if path == "" {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailurePoster)
		return nil
	}
	rel, err := s.client.DownloadImage(ctx, path, tmdb.PosterSize, s.cfg.DataDir)
	if err != nil {
		_ = s.db.RecordScrapeFailure(it.ID, store.FailurePoster, err.Error())
		return err
	}
	_ = s.db.ClearScrapeFailure(it.ID, store.FailurePoster)
	if rel == "" || rel == it.ImagePath {
		return nil
	}
	// 原本没有海报：只改这一条。走 ReplacePoster 的话空旧路径会命中所有空海报条目。
	if it.ImagePath == "" {
		return s.db.SetPoster(it.ID, rel)
	}
	if err := s.db.ReplacePoster(it.ImagePath, rel); err != nil {
		return err
	}
	s.removeOldPoster(it.ImagePath)
	return nil
}

// removeOldPoster 删掉已经没有任何条目引用的旧图。路径只允许 images/ 下的相对路径。
func (s *Scraper) removeOldPoster(rel string) {
	if !strings.HasPrefix(rel, "images/") || strings.Contains(rel, "..") {
		return
	}
	n, err := s.db.CountPosterRefs(rel)
	if err != nil || n > 0 {
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
		s.log.Warn("remove old poster failed", "path", rel, "err", err)
	}
}
