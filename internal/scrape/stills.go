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
)

// StillStatus 返回单集剧照回填进度。
func (s *Scraper) StillStatus() BackfillStatus {
	s.backdropMu.Lock()
	defer s.backdropMu.Unlock()
	return s.still
}

// StartStillBackfill 按当前尺寸重新下载单集剧照；与海报/背景图任务互斥。
func (s *Scraper) StartStillBackfill() bool {
	s.backdropMu.Lock()
	if s.backdrop.Running || s.poster.Running || s.still.Running {
		s.backdropMu.Unlock()
		return false
	}
	s.still = BackfillStatus{Running: true}
	s.backdropMu.Unlock()
	go s.runStillBackfill()
	return true
}

func (s *Scraper) runStillBackfill() {
	seasons, err := s.db.SeriesSeasonsForStills()
	if err != nil {
		s.finishStill(err)
		return
	}
	s.backdropMu.Lock()
	s.still.Total = len(seasons)
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
	jobs := make(chan store.SeriesSeason)
	var wg sync.WaitGroup
	var done, failed atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ss := range jobs {
				if err := s.refreshSeasonStills(ctx, ss); err != nil {
					failed.Add(1)
					s.log.Warn("episode stills refresh failed",
						"series", ss.SeriesID, "season", ss.Season, "err", err)
				}
				n := int(done.Add(1))
				s.backdropMu.Lock()
				s.still.Processed = n
				s.backdropMu.Unlock()
			}
		}()
	}

loop:
	for _, ss := range seasons {
		select {
		case jobs <- ss:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()

	if n := int64(len(seasons)); n > 0 && failed.Load() == n {
		s.finishStill(errors.New("全部单集剧照下载失败，检查 TMDB Key / 代理"))
		return
	}
	s.finishStill(nil)
}

func (s *Scraper) finishStill(err error) {
	s.backdropMu.Lock()
	s.still.Running = false
	s.still.LastFinished = time.Now().Unix()
	if err != nil {
		s.still.LastError = err.Error()
	}
	s.backdropMu.Unlock()
}

// refreshSeasonStills 按当前尺寸重下一季的剧照。替换成功后删掉旧尺寸文件，
// 每个单集只保留一个版本。
func (s *Scraper) refreshSeasonStills(ctx context.Context, ss store.SeriesSeason) error {
	sd, err := s.client.SeasonDetail(ctx, ss.TmdbID, ss.Season)
	if err != nil {
		return err
	}
	for _, ep := range sd.Episodes {
		if ep.StillPath == "" {
			continue
		}
		item, err := s.db.EpisodeByNumber(ss.SeriesID, ss.Season, ep.EpisodeNumber)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		still, err := s.client.DownloadImage(ctx, ep.StillPath, s.StillSize(), s.cfg.DataDir)
		if err != nil {
			s.log.Warn("download episode still failed", "item", item.ID, "err", err)
			continue
		}
		if still == "" || still == item.ImagePath {
			continue
		}
		old := item.ImagePath
		if err := s.db.UpdateItemImage(item.ID, still); err != nil {
			s.log.Warn("update episode still failed", "item", item.ID, "err", err)
			continue
		}
		s.removeReplacedStill(old, still)
	}
	if ss.SeasonID != "" {
		_ = s.db.MarkSeasonStillsChecked(ss.SeasonID)
	}
	s.advance()
	s.pace()
	return nil
}

// removeReplacedStill 删掉已被替换、且没有任何条目再引用的旧剧照文件。
func (s *Scraper) removeReplacedStill(old, current string) {
	if old == "" || old == current || !store.TMDBImagePath(old) || strings.Contains(old, "..") {
		return
	}
	n, err := s.db.CountPosterRefs(old)
	if err != nil || n > 0 {
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(old))); err != nil && !os.IsNotExist(err) {
		s.log.Warn("remove old episode still failed", "path", old, "err", err)
	}
}

// CleanupEpisodeStills 把单集封面改回剧集海报，并删除不再被引用的剧照文件。
// 返回删除的文件数。刮削或图片任务进行中时拒绝执行，避免删到正在写入的图。
func (s *Scraper) CleanupEpisodeStills() (int, error) {
	if s.maintenanceBusy() {
		return 0, errors.New("刮削或图片任务进行中")
	}

	paths, err := s.db.EpisodeStillPaths()
	if err != nil {
		return 0, err
	}
	if _, err := s.db.RepointEpisodeImagesToSeriesPoster(); err != nil {
		return 0, err
	}

	removed := 0
	for _, rel := range paths {
		if !store.TMDBImagePath(rel) || strings.Contains(rel, "..") {
			continue
		}
		refs, err := s.db.CountPosterRefs(rel)
		if err != nil || refs > 0 {
			continue
		}
		if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(rel))); err != nil {
			if !os.IsNotExist(err) {
				s.log.Warn("remove episode still failed", "path", rel, "err", err)
			}
			continue
		}
		removed++
	}
	s.log.Info("episode stills cleaned", "files", removed, "candidates", len(paths))
	return removed, nil
}
