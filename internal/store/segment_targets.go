package store

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// SegmentTarget 是「精准提取」里排队等待分析的一季。
type SegmentTarget struct {
	SeriesID string
	Season   int
	Status   string
	Note     string
}

// SeriesHit 是面板按剧名搜索时返回的一条剧集。
type SeriesHit struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Year     int    `json:"Year"`
	Seasons  int    `json:"Seasons"`
	Episodes int    `json:"Episodes"`
}

// AddSegmentTargets 把若干季加入精准提取队列。已存在的记录会重置为 queued，
// 方便失败后重新排队；created_at 保留首次入队时间，保证先来先跑。
func (d *DB) AddSegmentTargets(targets []SegmentTarget) (int, error) {
	if len(targets) == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
INSERT INTO segment_targets (series_id, season, status, note, created_at, updated_at)
VALUES (?, ?, 'queued', '', ?, ?)
ON CONFLICT(series_id, season) DO UPDATE SET
  status = 'queued',
  note = '',
  updated_at = excluded.updated_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	seen := make(map[string]bool, len(targets))
	added := 0
	for _, t := range targets {
		if t.SeriesID == "" || t.Season <= 0 {
			continue
		}
		key := t.SeriesID + "|" + strconv.Itoa(t.Season)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := stmt.Exec(t.SeriesID, t.Season, now, now); err != nil {
			return 0, err
		}
		added++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// NextQueuedSegmentTarget 取队列里最早的一季；没有任务时返回 nil。
func (d *DB) NextQueuedSegmentTarget() (*SegmentTarget, error) {
	var t SegmentTarget
	err := d.QueryRow(`
SELECT series_id, season, status, note FROM segment_targets
WHERE status = 'queued'
ORDER BY created_at, series_id, season
LIMIT 1`).Scan(&t.SeriesID, &t.Season, &t.Status, &t.Note)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// SetSegmentTargetStatus 更新队列状态；目标不存在时静默忽略。
func (d *DB) SetSegmentTargetStatus(seriesID string, season int, status, note string) error {
	_, err := d.Exec(`
UPDATE segment_targets SET status = ?, note = ?, updated_at = ?
WHERE series_id = ? AND season = ?`, status, note, time.Now().Unix(), seriesID, season)
	return err
}

// ResetStaleSegmentTargets 把上次进程中断留下的 running 重新排队。
func (d *DB) ResetStaleSegmentTargets() error {
	_, err := d.Exec(`UPDATE segment_targets
SET status = 'queued', note = 'interrupted', updated_at = ?
WHERE status = 'running'`, time.Now().Unix())
	return err
}

// SegmentTargetCounts 返回各状态的数量，面板显示队列进度用。
func (d *DB) SegmentTargetCounts() (map[string]int, error) {
	rows, err := d.Query(`SELECT status, COUNT(*) FROM segment_targets GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int, 4)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// SeasonsNeedingSegments 返回某部剧里还缺片头/片尾、且至少有两集可播放的季。
// 精准提取直接用它把整部剧排进队列。
func (d *DB) SeasonsNeedingSegments(seriesID string) ([]int, error) {
	rows, err := d.Query(`
SELECT e.parent_index_number
FROM items e
WHERE e.type = 'Episode' AND e.series_id = ? AND e.parent_index_number > 0
  AND EXISTS (SELECT 1 FROM media_sources ms
              WHERE ms.item_id = e.id AND ms.url <> '')
  AND NOT EXISTS (
    SELECT 1 FROM media_segments m
    JOIN items me ON me.id = m.item_id
    WHERE me.type = 'Episode'
      AND me.series_id = e.series_id
      AND me.parent_index_number = e.parent_index_number
      AND m.type IN ('Intro', 'Outro'))
GROUP BY e.parent_index_number
HAVING COUNT(*) >= 2
ORDER BY e.parent_index_number`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int, 0, 8)
	for rows.Next() {
		var season int
		if err := rows.Scan(&season); err != nil {
			return nil, err
		}
		out = append(out, season)
	}
	return out, rows.Err()
}

// SearchSeries 按剧名/原名/排序名模糊搜索，面板精准提取用。
func (d *DB) SearchSeries(q string, limit int) ([]SeriesHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	like := "%" + escapeLike(q) + "%"
	rows, err := d.Query(`
SELECT i.id, i.name, i.production_year,
       (SELECT COUNT(DISTINCT e.parent_index_number) FROM items e
        WHERE e.series_id = i.id AND e.type = 'Episode' AND e.parent_index_number > 0),
       (SELECT COUNT(*) FROM items e
        WHERE e.series_id = i.id AND e.type = 'Episode')
FROM items i
WHERE i.type = 'Series'
  AND (i.name LIKE ? ESCAPE '\' OR i.original_title LIKE ? ESCAPE '\'
       OR i.sort_name LIKE ? ESCAPE '\')
ORDER BY i.name
LIMIT ?`, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SeriesHit, 0, limit)
	for rows.Next() {
		var h SeriesHit
		if err := rows.Scan(&h.ID, &h.Name, &h.Year, &h.Seasons, &h.Episodes); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// escapeLike 转义 LIKE 通配符，用户输入的 % 和 _ 按字面匹配。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
