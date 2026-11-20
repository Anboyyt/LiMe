package scrape

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"lime/internal/store"
	"lime/internal/tmdb"
)

// Search 按标题搜索 TMDB，供详情页手动识别使用。
func (s *Scraper) Search(ctx context.Context, kind, query, year string) ([]tmdb.SearchResult, error) {
	if !s.Enabled() {
		return nil, errors.New("tmdb api key not configured")
	}
	return s.client.Search(ctx, kind, query, year)
}

// ImageURL 拼出 TMDB 图片地址，供搜索结果缩略图使用。
func (s *Scraper) ImageURL(path, size string) string {
	if s.client == nil {
		return ""
	}
	return s.client.ImageURL(path, size)
}

// Reidentify 手动指定 TMDB ID 并触发重新刮削。电影直接重刮；
// 剧集连同所有季一起重刮，单集由季刮削顺带更新。
func (s *Scraper) Reidentify(itemID, tmdbID string) error {
	if !s.Enabled() {
		return errors.New("tmdb api key not configured")
	}
	tmdbID = strings.TrimSpace(tmdbID)
	if tmdbID == "" {
		return errors.New("TMDB ID 不能为空")
	}
	if _, err := strconv.Atoi(tmdbID); err != nil {
		return errors.New("TMDB ID 必须是数字")
	}

	it, err := s.db.GetItem(itemID)
	if err != nil {
		return err
	}

	switch it.Type {
	case store.TypeMovie:
		if err := s.db.SetItemTmdbID(it.ID, tmdbID); err != nil {
			return err
		}
		if err := s.db.ResetItemScrape(it.ID); err != nil {
			return err
		}
		go s.scrapeIdentified([]string{it.ID}, it.Name)
	case store.TypeSeries:
		seasons, err := s.db.SeasonIDsForSeries(it.ID)
		if err != nil {
			return err
		}
		if err := s.db.SetItemTmdbID(it.ID, tmdbID); err != nil {
			return err
		}
		if err := s.db.ResetSeriesScrape(it.ID); err != nil {
			return err
		}
		ids := make([]string, 0, len(seasons)+1)
		ids = append(ids, it.ID)
		ids = append(ids, seasons...)
		go s.scrapeIdentified(ids, it.Name)
	default:
		return errors.New("只有电影和剧集支持手动识别")
	}
	return nil
}

func (s *Scraper) scrapeIdentified(ids []string, name string) {
	n, err := s.ScrapeItems(ids)
	if err != nil {
		s.log.Warn("identify scrape failed", "name", name, "err", err)
		return
	}
	s.log.Info("identify scrape done", "name", name, "items", n)
}
