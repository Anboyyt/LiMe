package store

import (
	"strings"
	"time"
)

// 刮削失败类型。同一条目可以同时有多条（比如 ID 失效 + 海报没下到）。
const (
	FailureNotFound = "not_found" // TMDB 返回 404：ID 失效或被合并
	FailureAPI      = "api_error" // 其他接口错误：网络、限流、5xx
	FailurePoster   = "poster"    // 海报下载失败
	FailureBackdrop = "backdrop"  // 背景图下载失败
	FailureLogo     = "logo"      // 标题图下载失败
)

// ScrapeFailure 是报告里的一行。
type ScrapeFailure struct {
	ItemID    string `json:"ItemId"`
	Name      string `json:"Name"`
	Type      string `json:"Type"`
	TmdbID    string `json:"TmdbId"`
	Path      string `json:"Path"`
	Kind      string `json:"Kind"`
	Detail    string `json:"Detail"`
	UpdatedAt int64  `json:"UpdatedAt"`
}

// ScrapeHealth 是没法从失败表里直接看出来的存量问题计数。
type ScrapeHealth struct {
	NoTmdbID        int `json:"NoTmdbId"`
	Unscraped       int `json:"Unscraped"`
	MissingPoster   int `json:"MissingPoster"`
	MissingBackdrop int `json:"MissingBackdrop"`
}

// RecordScrapeFailure 记录（或覆盖）某个条目某类失败。detail 只留前 400 字。
func (d *DB) RecordScrapeFailure(itemID, kind, detail string) error {
	if itemID == "" || kind == "" {
		return nil
	}
	if len(detail) > 400 {
		detail = detail[:400]
	}
	_, err := d.Exec(
		`INSERT INTO scrape_failures(item_id, kind, detail, updated_at)
		 VALUES(?, ?, ?, ?)
		 ON CONFLICT(item_id, kind) DO UPDATE SET
		   detail = excluded.detail, updated_at = excluded.updated_at`,
		itemID, kind, detail, time.Now().Unix())
	return err
}

// ClearScrapeFailure 清除条目的一类或多类失败记录。kinds 为空时清全部。
func (d *DB) ClearScrapeFailure(itemID string, kinds ...string) error {
	if itemID == "" {
		return nil
	}
	if len(kinds) == 0 {
		_, err := d.Exec(`DELETE FROM scrape_failures WHERE item_id = ?`, itemID)
		return err
	}
	ph := make([]string, len(kinds))
	args := make([]any, 0, len(kinds)+1)
	args = append(args, itemID)
	for i, k := range kinds {
		ph[i] = "?"
		args = append(args, k)
	}
	_, err := d.Exec(
		`DELETE FROM scrape_failures WHERE item_id = ? AND kind IN (`+strings.Join(ph, ",")+`)`,
		args...)
	return err
}

// ScrapeFailureCounts 按类型统计失败条目数。
func (d *DB) ScrapeFailureCounts() (map[string]int, error) {
	rows, err := d.Query(`SELECT kind, COUNT(*) FROM scrape_failures GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ScrapeFailures 返回失败列表（最新的在前），带条目名和第一条片源路径。
func (d *DB) ScrapeFailures(limit int) ([]ScrapeFailure, error) {
	if limit <= 0 || limit > 50000 {
		limit = 5000
	}
	rows, err := d.Query(`
		SELECT f.item_id,
		       COALESCE(i.name, ''), COALESCE(i.type, ''), COALESCE(i.tmdb_id, ''),
		       COALESCE((SELECT s.strm_path FROM media_sources s WHERE s.item_id = i.id LIMIT 1), ''),
		       f.kind, f.detail, f.updated_at
		  FROM scrape_failures f
		  LEFT JOIN items i ON i.id = f.item_id
		 ORDER BY f.updated_at DESC, f.item_id
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScrapeFailure
	for rows.Next() {
		var f ScrapeFailure
		if err := rows.Scan(&f.ItemID, &f.Name, &f.Type, &f.TmdbID, &f.Path,
			&f.Kind, &f.Detail, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ScrapeHealth 统计「没 ID / 没刮 / 缺图」的存量条目。这些不是接口报错，
// 但同样是刮削没覆盖到的地方，报告里一起给出来。
func (d *DB) ScrapeHealth() (ScrapeHealth, error) {
	var h ScrapeHealth
	err := d.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN (tmdb_id IS NULL OR tmdb_id = '') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN tmdb_id <> '' AND scraped_at = 0 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN (image_path IS NULL OR image_path = '') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN (backdrop_path IS NULL OR backdrop_path = '') THEN 1 ELSE 0 END), 0)
		FROM items WHERE type IN (?, ?)`,
		TypeMovie, TypeSeries).Scan(&h.NoTmdbID, &h.Unscraped, &h.MissingPoster, &h.MissingBackdrop)
	return h, err
}
