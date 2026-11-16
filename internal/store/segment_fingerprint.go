package store

import (
	"strings"
	"time"
)

// FingerprintCandidate 是一季还没跑过音频指纹（或已过重试间隔）的剧集。
type FingerprintCandidate struct {
	SeriesID string
	Season   int
	Episodes int
}

// FingerprintCandidates 找出指定范围内、整季都没有 Intro/Outro 片段的季。
// 只返回至少有 2 集可播放的季；音频指纹本身就是靠多集重复片段找边界。
func (d *DB) FingerprintCandidates(mode string, since, retryAfter int64, limit int) ([]FingerprintCandidate, error) {
	if limit <= 0 {
		limit = 1
	}
	args := make([]any, 0, 8)
	scope, err := d.fingerprintScopeWhere(mode, since, &args)
	if err != nil {
		return nil, err
	}

	q := `
SELECT e.series_id, e.parent_index_number, COUNT(*)
FROM items e
WHERE e.type = 'Episode' AND e.parent_index_number > 0
  AND EXISTS (SELECT 1 FROM media_sources ms
              WHERE ms.item_id = e.id AND ms.url <> '')
  AND ` + scope + `
  AND NOT EXISTS (
    SELECT 1 FROM media_segments m
    JOIN items me ON me.id = m.item_id
    WHERE me.type = 'Episode'
      AND me.series_id = e.series_id
      AND me.parent_index_number = e.parent_index_number
      AND m.type IN ('Intro', 'Outro'))
  AND NOT EXISTS (
    SELECT 1 FROM segment_fingerprint_runs r
    WHERE r.series_id = e.series_id
      AND r.season = e.parent_index_number
      AND r.updated_at > ?)
GROUP BY e.series_id, e.parent_index_number
HAVING COUNT(*) >= 2
ORDER BY MAX(e.created_at) DESC
LIMIT ?`
	args = append(args, retryAfter, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]FingerprintCandidate, 0, limit)
	for rows.Next() {
		var c FingerprintCandidate
		if err := rows.Scan(&c.SeriesID, &c.Season, &c.Episodes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fingerprintScopeWhere 生成回填范围条件；favorites/watching 复用很小的收藏和在追集合。
func (d *DB) fingerprintScopeWhere(mode string, since int64, args *[]any) (string, error) {
	switch strings.ToLower(mode) {
	case "all":
		return "1 = 1", nil
	case "recent":
		if since <= 0 {
			return "1 = 1", nil
		}
		*args = append(*args, since)
		return `(e.created_at >= ? OR EXISTS (
  SELECT 1 FROM playstates p WHERE p.item_id = e.id AND p.position_ticks > 0))`, nil
	case "active":
		active, aerr := d.activeSeriesIDsSince(
			time.Now().Add(-segmentActiveWindow).Unix(), segmentActiveMinTicks)
		if aerr != nil || len(active) == 0 {
			return "1 = 0", nil
		}
		for _, id := range active {
			*args = append(*args, id)
		}
		return `e.series_id IN (` + placeholders(len(active)) + `)`, nil
	case "watching", "favorites":
		items, series, err := d.favoriteIDs()
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, 3)
		if len(series) > 0 {
			parts = append(parts, `e.series_id IN (`+placeholders(len(series))+`)`)
			for _, id := range series {
				*args = append(*args, id)
			}
		}
		if len(items) > 0 {
			parts = append(parts, `e.id IN (`+placeholders(len(items))+`)`)
			for _, id := range items {
				*args = append(*args, id)
			}
		}
		if strings.EqualFold(mode, "watching") {
			if active, aerr := d.activeSeriesIDs(time.Now().Add(-14 * 24 * time.Hour).Unix()); aerr == nil && len(active) > 0 {
				parts = append(parts, `e.series_id IN (`+placeholders(len(active))+`)`)
				for _, id := range active {
					*args = append(*args, id)
				}
			}
		}
		if len(parts) == 0 {
			return "1 = 0", nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	default:
		return "1 = 0", nil
	}
}

// ResetStaleFingerprintRuns 把上次进程中断留下的 running 标记清掉，
// 否则这一季会被回填查询跳过 7 天。
func (d *DB) ResetStaleFingerprintRuns() error {
	_, err := d.Exec(`UPDATE segment_fingerprint_runs
SET status = 'failed', note = 'interrupted', updated_at = 0
WHERE status = 'running'`)
	return err
}

// MarkFingerprintRun 记录某一季的指纹尝试，回填调度靠它避免反复扫没有 OP/ED 的剧。
func (d *DB) MarkFingerprintRun(seriesID string, season int, status, note string) error {
	_, err := d.Exec(`
INSERT INTO segment_fingerprint_runs (series_id, season, status, note, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(series_id, season) DO UPDATE SET
  status = excluded.status,
  note = excluded.note,
  updated_at = excluded.updated_at`,
		seriesID, season, status, note, time.Now().Unix())
	return err
}
