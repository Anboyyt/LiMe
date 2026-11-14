package store

import (
	"fmt"
	"strings"
	"time"
)

// 片段类型，取值与 Jellyfin 的 MediaSegmentType 一致。
const (
	SegmentIntro      = "Intro"
	SegmentOutro      = "Outro"
	SegmentRecap      = "Recap"
	SegmentPreview    = "Preview"
	SegmentCommercial = "Commercial"
)

// MediaSegment 是一条片头/片尾片段，时间单位是 100ns ticks。
type MediaSegment struct {
	ID         string
	ItemID     string
	Type       string
	StartTicks int64
	EndTicks   int64
	Source     string // chapter / fingerprint / manual
	Confidence float64
}

// ReplaceMediaSegments 用一次分析结果替换条目的自动片段（source != manual）。
// 手动片段保留，避免自动扫描覆盖用户编辑。segs 为空时同样记录分析状态，
// 防止预热反复对没有章节的剧跑 ffprobe。
func (d *DB) ReplaceMediaSegments(itemID string, segs []MediaSegment, method, note string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM media_segments WHERE item_id = ? AND source <> 'manual'`, itemID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for i := range segs {
		s := segs[i]
		if s.Type == "" || s.StartTicks < 0 || s.EndTicks <= s.StartTicks {
			continue
		}
		if s.Source == "" {
			s.Source = "chapter"
		}
		if s.Confidence <= 0 {
			s.Confidence = 1
		}
		if _, err := tx.Exec(`
INSERT INTO media_segments (id, item_id, type, start_ticks, end_ticks, source, confidence, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			segmentID(itemID, s.Type, s.StartTicks), itemID, s.Type,
			s.StartTicks, s.EndTicks, s.Source, s.Confidence, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
INSERT INTO segment_analysis (item_id, status, method, note, updated_at)
VALUES (?, 'done', ?, ?, ?)
ON CONFLICT(item_id) DO UPDATE SET status = 'done', method = excluded.method,
  note = excluded.note, updated_at = excluded.updated_at`,
		itemID, method, note, now); err != nil {
		return err
	}
	return tx.Commit()
}

func segmentID(itemID, typ string, startTicks int64) string {
	return fmt.Sprintf("%s-%s-%d", itemID, strings.ToLower(typ), startTicks)
}

// SetSegmentAnalysisFailure 记录一次失败，失败条目隔一段时间再重试。
func (d *DB) SetSegmentAnalysisFailure(itemID, method, note string) error {
	_, err := d.Exec(`
INSERT INTO segment_analysis (item_id, status, method, note, updated_at)
VALUES (?, 'failed', ?, ?, ?)
ON CONFLICT(item_id) DO UPDATE SET status = 'failed', method = excluded.method,
  note = excluded.note, updated_at = excluded.updated_at`,
		itemID, method, note, time.Now().Unix())
	return err
}

// SegmentAnalysisDone 表示这个条目的片段分析已经完成（无论有没有结果）。
func (d *DB) SegmentAnalysisDone(itemID string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM segment_analysis WHERE item_id = ? AND status = 'done'`, itemID).Scan(&n)
	return n > 0, err
}

// MediaSegmentsForItem 返回条目的片段，types 为空表示不过滤。
func (d *DB) MediaSegmentsForItem(itemID string, types []string) ([]MediaSegment, error) {
	q := `SELECT id, item_id, type, start_ticks, end_ticks, source, confidence
FROM media_segments WHERE item_id = ?`
	args := []any{itemID}
	if len(types) > 0 {
		q += ` AND type IN (` + placeholders(len(types)) + `)`
		for _, t := range types {
			args = append(args, t)
		}
	}
	q += ` ORDER BY start_ticks`

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MediaSegment, 0, 4)
	for rows.Next() {
		var s MediaSegment
		if err := rows.Scan(&s.ID, &s.ItemID, &s.Type, &s.StartTicks, &s.EndTicks, &s.Source, &s.Confidence); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// segmentScopeWhere 生成片段回填的范围条件。args 会按顺序追加。
// primaryOnly 限定每个条目只取一个主片源，避免多版本重复分析。
func (d *DB) segmentScopeWhere(mode string, since int64, args *[]any) string {
	// 失败条目隔 24 小时再试，避免坏文件每晚被反复扫描。
	*args = append(*args, time.Now().Add(-24*time.Hour).Unix())
	where := `ms.url <> ''
  AND i.type IN ('Episode', 'Movie')
  AND ms.id = (SELECT ms2.id FROM media_sources ms2
               WHERE ms2.item_id = ms.item_id AND ms2.url <> ''
               ORDER BY ms2.sort_rank, ms2.size DESC, ms2.id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM segment_analysis sa
                  WHERE sa.item_id = i.id
                    AND (sa.status = 'done' OR (sa.status = 'failed' AND sa.updated_at > ?)))`

	switch strings.ToLower(mode) {
	case "all":
	case "recent":
		if since > 0 {
			where += ` AND (i.created_at >= ? OR EXISTS (
        SELECT 1 FROM playstates p WHERE p.item_id = i.id AND p.position_ticks > 0))`
			*args = append(*args, since)
		}
	case "watching", "favorites":
		items, series, err := d.favoriteIDs()
		parts := make([]string, 0, 3)
		if err == nil && len(items) > 0 {
			parts = append(parts, `i.id IN (`+placeholders(len(items))+`)`)
			for _, id := range items {
				*args = append(*args, id)
			}
		}
		if err == nil && len(series) > 0 {
			parts = append(parts, `(i.type = 'Episode' AND i.series_id IN (`+placeholders(len(series))+`))`)
			for _, id := range series {
				*args = append(*args, id)
			}
		}
		if strings.EqualFold(mode, "watching") {
			if active, aerr := d.activeSeriesIDs(time.Now().Add(-14 * 24 * time.Hour).Unix()); aerr == nil && len(active) > 0 {
				parts = append(parts, `(i.type = 'Episode' AND i.series_id IN (`+placeholders(len(active))+`))`)
				for _, id := range active {
					*args = append(*args, id)
				}
			}
		}
		if len(parts) == 0 {
			return where + ` AND 1 = 0`
		}
		where += ` AND (` + strings.Join(parts, " OR ") + `)`
	default:
		return where + ` AND 1 = 0`
	}
	return where
}

const segmentSourceSelect = `
SELECT ms.id, ms.item_id, ms.strm_path, ms.url, ms.cloud, ms.drive_id, ms.cloud_file_id, ms.sha1_etag,
       ms.size, ms.container, ms.file_name, ms.version_name, ms.sort_rank,
       ms.duration_ticks, ms.probe_status, ms.strm_mtime, ms.strm_size, ms.updated_at
FROM media_sources ms
JOIN items i ON i.id = ms.item_id` + segmentWatchJoin

// 回填优先级：没看过（0）→ 在看（1）→ 已完成（2）。
// 一部剧里先把还没看的集补上，看完的放最后。
const segmentWatchJoin = `
LEFT JOIN (
    SELECT item_id, MAX(played) AS played, MAX(position_ticks) AS position
    FROM playstates GROUP BY item_id
) ps ON ps.item_id = i.id`

const segmentWatchOrder = `CASE
    WHEN ps.item_id IS NULL OR (ps.played = 0 AND ps.position = 0) THEN 0
    WHEN ps.played = 1 THEN 2
    ELSE 1
  END`

// SourcesForSegmentWarmup 返回需要补片头片尾的片源（只取每个条目的主片源）。
func (d *DB) SourcesForSegmentWarmup(since int64, limit int, mode string) ([]*MediaSource, error) {
	if limit <= 0 {
		limit = 50
	}
	args := make([]any, 0, 8)
	where := d.segmentScopeWhere(mode, since, &args)
	q := segmentSourceSelect + ` WHERE ` + where + ` ORDER BY ` + segmentWatchOrder + `, i.created_at DESC LIMIT ?`
	args = append(args, limit)
	return d.querySegmentSources(q, args...)
}

// SourcesForSeriesSegmentBackfill 返回某部剧里还没做片段分析的集（主片源）。
func (d *DB) SourcesForSeriesSegmentBackfill(seriesID string, limit int) ([]*MediaSource, error) {
	if seriesID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	q := segmentSourceSelect + ` WHERE ms.url <> ''
  AND i.type = 'Episode' AND i.series_id = ?
  AND ms.id = (SELECT ms2.id FROM media_sources ms2
               WHERE ms2.item_id = ms.item_id AND ms2.url <> ''
               ORDER BY ms2.sort_rank, ms2.size DESC, ms2.id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM segment_analysis sa
                  WHERE sa.item_id = i.id
                    AND (sa.status = 'done' OR (sa.status = 'failed' AND sa.updated_at > ?)))
ORDER BY ` + segmentWatchOrder + `, i.parent_index_number, i.index_number LIMIT ?`
	return d.querySegmentSources(q, seriesID, time.Now().Add(-24*time.Hour).Unix(), limit)
}

// SegmentPendingCount 返回还没做片段分析的条目数，给面板显示。
func (d *DB) SegmentPendingCount() (int, error) {
	var n int
	err := d.QueryRow(`
SELECT COUNT(*) FROM items i
WHERE i.type IN ('Episode', 'Movie')
  AND EXISTS (SELECT 1 FROM media_sources ms WHERE ms.item_id = i.id AND ms.url <> '')
  AND NOT EXISTS (SELECT 1 FROM segment_analysis sa WHERE sa.item_id = i.id AND sa.status = 'done')`).Scan(&n)
	return n, err
}

func (d *DB) querySegmentSources(q string, args ...any) ([]*MediaSource, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*MediaSource, 0, 32)
	for rows.Next() {
		var (
			s   MediaSource
			upd int64
		)
		if err := rows.Scan(
			&s.ID, &s.ItemID, &s.StrmPath, &s.URL, &s.Cloud, &s.DriveID, &s.CloudFileID,
			&s.SHA1ETag, &s.Size, &s.Container, &s.FileName, &s.VersionName, &s.SortRank,
			&s.DurationTicks, &s.ProbeStatus, &s.StrmMtime, &s.StrmSize, &upd,
		); err != nil {
			return nil, err
		}
		s.UpdatedAt = time.Unix(upd, 0)
		out = append(out, &s)
	}
	return out, rows.Err()
}

// SetManualSegments 用用户手动标记替换条目的全部片段。
// 手动标记优先于自动扫描：同时写 segment_analysis，避免预热再把自动结果加回来。
func (d *DB) SetManualSegments(itemID string, segs []MediaSegment) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM media_segments WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for i := range segs {
		s := segs[i]
		if s.Type == "" || s.StartTicks < 0 || s.EndTicks <= s.StartTicks {
			continue
		}
		if _, err := tx.Exec(`
INSERT INTO media_segments (id, item_id, type, start_ticks, end_ticks, source, confidence, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'manual', 1, ?, ?)`,
			segmentID(itemID, s.Type, s.StartTicks)+"-manual", itemID, s.Type,
			s.StartTicks, s.EndTicks, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
INSERT INTO segment_analysis (item_id, status, method, note, updated_at)
VALUES (?, 'done', 'manual', 'user marked', ?)
ON CONFLICT(item_id) DO UPDATE SET status = 'done', method = 'manual',
  note = 'user marked', updated_at = excluded.updated_at`,
		itemID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SeasonEpisodeSource 是某季一集的主片源，音频指纹分析用。
type SeasonEpisodeSource struct {
	ItemID       string
	IndexNumber  int
	RunTimeTicks int64
	URL          string
}

// SeasonSources 返回某部剧某一季每个条目（单集）的主片源，按集号排序。
func (d *DB) SeasonSources(seriesID string, season int) ([]SeasonEpisodeSource, error) {
	rows, err := d.Query(`
SELECT e.id, e.index_number, e.run_time_ticks, ms.url
FROM items e
JOIN media_sources ms ON ms.item_id = e.id
WHERE e.type = 'Episode' AND e.series_id = ? AND e.parent_index_number = ?
  AND ms.url <> ''
  AND ms.id = (SELECT ms2.id FROM media_sources ms2
               WHERE ms2.item_id = e.id AND ms2.url <> ''
               ORDER BY ms2.sort_rank, ms2.size DESC, ms2.id LIMIT 1)
ORDER BY e.index_number`, seriesID, season)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SeasonEpisodeSource, 0, 16)
	for rows.Next() {
		var s SeasonEpisodeSource
		if err := rows.Scan(&s.ItemID, &s.IndexNumber, &s.RunTimeTicks, &s.URL); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SeasonHasSegments 判断某一季是否已经有任何片段（章节/手动/指纹）。
func (d *DB) SeasonHasSegments(seriesID string, season int) (bool, error) {
	var n int
	err := d.QueryRow(`
SELECT COUNT(*) FROM media_segments ms
JOIN items e ON e.id = ms.item_id
WHERE e.series_id = ? AND e.parent_index_number = ? LIMIT 1`, seriesID, season).Scan(&n)
	return n > 0, err
}
