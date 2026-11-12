package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type MediaSource struct {
	ID            string
	ItemID        string
	StrmPath      string
	URL           string
	Cloud         string
	DriveID       string
	CloudFileID   string
	SHA1ETag      string
	Size          int64
	Container     string
	FileName      string
	VersionName   string
	SortRank      int
	DurationTicks int64
	ProbeStatus   string
	StrmMtime     int64
	StrmSize      int64
	UpdatedAt     time.Time
}

type MediaStream struct {
	SourceID       string
	Index          int
	Type           string
	Codec          string
	Width          int
	Height         int
	BitRate        int64
	BitDepth       int
	Channels       int
	FrameRate      float64
	Language       string
	Title          string
	VideoRange     string
	Profile        string
	Level          float64
	AspectRatio    string
	IsInterlaced   bool
	ColorPrimaries string
	ColorTransfer  string
	ColorSpace     string
	PixelFormat    string
	SampleRate     int
	ChannelLayout  string
	IsDefault      bool
	IsForced       bool
}

func (d *DB) UpsertMediaSource(src *MediaSource) error { return upsertMediaSource(d.DB, src) }

func upsertMediaSource(e execer, src *MediaSource) error {
	now := time.Now().Unix()

	// 同一路径最多只能有一条片源：STRM 被重新生成（file_id 变化）时，
	// 旧行必须先删掉，否则客户端会看到重复/失效的版本。
	if src.StrmPath != "" {
		if _, err := e.Exec(
			`DELETE FROM media_sources WHERE strm_path = ? AND id <> ?`,
			src.StrmPath, src.ID,
		); err != nil {
			return err
		}
	}
	// 条目 ID 变化（目录改名、NFO 标题变化）会让 source ID 跟着变，
	// 但 (drive_id, cloud_file_id) 唯一索引仍指向旧行。先清掉旧行，
	// 否则 INSERT 会报 UNIQUE constraint failed，条目变成「有卡片没片源」。
	//
	// `cloud_file_id <> ''` 不能省：idx_media_sources_cloud 是部分索引，
	// 少了这个条件 SQLite 用不上它，每条片源都要全表扫 29 万行，全量扫描会退化成 O(n²)。
	if src.CloudFileID != "" {
		if _, err := e.Exec(
			`DELETE FROM media_sources
WHERE drive_id = ? AND cloud_file_id = ? AND cloud_file_id <> '' AND id <> ?`,
			src.DriveID, src.CloudFileID, src.ID,
		); err != nil {
			return err
		}
	}

	_, err := e.Exec(`
INSERT INTO media_sources (
  id, item_id, strm_path, url, cloud, drive_id, cloud_file_id, sha1_etag,
  size, container, file_name, version_name, sort_rank,
  duration_ticks, probe_status, strm_mtime, strm_size, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  item_id = excluded.item_id,
  strm_path = excluded.strm_path,
  url = excluded.url,
  cloud = excluded.cloud,
  drive_id = excluded.drive_id,
  cloud_file_id = excluded.cloud_file_id,
  sha1_etag = excluded.sha1_etag,
  size = excluded.size,
  container = excluded.container,
  file_name = excluded.file_name,
  version_name = excluded.version_name,
  sort_rank = excluded.sort_rank,
  duration_ticks = MAX(excluded.duration_ticks, media_sources.duration_ticks),
  -- 探测结果只由 prober 更新，扫描不能把 done 重置成 none
  probe_status = CASE WHEN media_sources.probe_status = 'done'
                      THEN media_sources.probe_status ELSE excluded.probe_status END,
  strm_mtime = excluded.strm_mtime,
  strm_size = excluded.strm_size,
  updated_at = excluded.updated_at`,
		src.ID, src.ItemID, src.StrmPath, src.URL, src.Cloud, src.DriveID, src.CloudFileID, src.SHA1ETag,
		src.Size, src.Container, src.FileName, src.VersionName, src.SortRank,
		src.DurationTicks, src.ProbeStatus, src.StrmMtime, src.StrmSize, now,
	)
	return err
}

func (d *DB) ReplaceMediaStreams(sourceID string, streams []MediaStream) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := replaceMediaStreams(tx, sourceID, streams); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProbeResult 记录探测状态和真实时长。
func (d *DB) SetProbeResult(sourceID, status string, durationTicks int64) error {
	_, err := d.Exec(`
UPDATE media_sources SET probe_status = ?,
  duration_ticks = CASE WHEN ? > 0 THEN ? ELSE duration_ticks END,
  updated_at = ?
WHERE id = ?`, status, durationTicks, durationTicks, time.Now().Unix(), sourceID)
	return err
}

// FillItemRuntimeFromSources 用已探测到的片源时长回填条目时长。
// items.run_time_ticks 是客户端画进度条、以及 95% 完成判定用的；
// 只补空值，不覆盖刮削出来的值，避免和 TMDB 数据来回打架。
// 多版本（1080p/2160p）取最大值，保证先后探测不会让时长来回跳。
func (d *DB) FillItemRuntimeFromSources(itemID string) error {
	_, err := d.Exec(`
UPDATE items SET run_time_ticks = (
  SELECT MAX(ms.duration_ticks) FROM media_sources ms
  WHERE ms.item_id = items.id AND ms.probe_status = 'done' AND ms.duration_ticks > 0
)
WHERE id = ? AND run_time_ticks = 0
  AND EXISTS (
    SELECT 1 FROM media_sources ms
    WHERE ms.item_id = items.id AND ms.probe_status = 'done' AND ms.duration_ticks > 0
  )`, itemID)
	return err
}

func replaceMediaStreams(e execer, sourceID string, streams []MediaStream) error {
	if _, err := e.Exec(`DELETE FROM media_streams WHERE source_id = ?`, sourceID); err != nil {
		return err
	}
	for _, s := range streams {
		if _, err := e.Exec(`
INSERT INTO media_streams (
  source_id, stream_index, type, codec, width, height, bit_rate, bit_depth, channels,
  frame_rate, language, title, video_range, profile, level, aspect_ratio, is_interlaced,
  color_primaries, color_transfer, color_space, pixel_format, sample_rate, channel_layout,
  is_default, is_forced
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			sourceID, s.Index, s.Type, s.Codec, s.Width, s.Height, s.BitRate, s.BitDepth, s.Channels,
			s.FrameRate, s.Language, s.Title, s.VideoRange, s.Profile, s.Level, s.AspectRatio,
			boolToInt(s.IsInterlaced), s.ColorPrimaries, s.ColorTransfer, s.ColorSpace,
			s.PixelFormat, s.SampleRate, s.ChannelLayout, boolToInt(s.IsDefault), boolToInt(s.IsForced),
		); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) MediaSourcesByItem(itemID string) ([]*MediaSource, error) {
	rows, err := d.Query(`
SELECT id, item_id, strm_path, url, cloud, drive_id, cloud_file_id, sha1_etag,
       size, container, file_name, version_name, sort_rank,
       duration_ticks, probe_status, strm_mtime, strm_size, updated_at
FROM media_sources WHERE item_id = ? ORDER BY sort_rank, size DESC, id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*MediaSource, 0, 2)
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

// MediaSourcesByItems 批量查询片源，按 item_id 分组，用于列表接口
// 一次性补齐 MediaSources / MediaSourceCount。
func (d *DB) MediaSourcesByItems(itemIDs []string) (map[string][]*MediaSource, error) {
	out := make(map[string][]*MediaSource, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}

	args := make([]any, 0, len(itemIDs))
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := d.Query(`
SELECT id, item_id, strm_path, url, cloud, drive_id, cloud_file_id, sha1_etag,
       size, container, file_name, version_name, sort_rank,
       duration_ticks, probe_status, strm_mtime, strm_size, updated_at
FROM media_sources WHERE item_id IN (`+placeholders(len(itemIDs))+`)
ORDER BY item_id, sort_rank, size DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
		out[s.ItemID] = append(out[s.ItemID], &s)
	}
	return out, rows.Err()
}

func (d *DB) GetMediaSource(id string) (*MediaSource, error) {
	var (
		s   MediaSource
		upd int64
	)
	err := d.QueryRow(`
SELECT id, item_id, strm_path, url, cloud, drive_id, cloud_file_id, sha1_etag,
       size, container, file_name, version_name, sort_rank,
       duration_ticks, probe_status, strm_mtime, strm_size, updated_at
FROM media_sources WHERE id = ?`, id).Scan(
		&s.ID, &s.ItemID, &s.StrmPath, &s.URL, &s.Cloud, &s.DriveID, &s.CloudFileID,
		&s.SHA1ETag, &s.Size, &s.Container, &s.FileName, &s.VersionName, &s.SortRank,
		&s.DurationTicks, &s.ProbeStatus, &s.StrmMtime, &s.StrmSize, &upd,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.UpdatedAt = time.Unix(upd, 0)
	return &s, nil
}

func (d *DB) MediaStreamsBySource(sourceID string) ([]*MediaStream, error) {
	rows, err := d.Query(`
SELECT source_id, stream_index, type, codec, width, height, bit_rate, bit_depth, channels,
       frame_rate, language, title, video_range, profile, level, aspect_ratio, is_interlaced,
       color_primaries, color_transfer, color_space, pixel_format, sample_rate, channel_layout,
       is_default, is_forced
FROM media_streams WHERE source_id = ? ORDER BY stream_index`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*MediaStream, 0, 4)
	for rows.Next() {
		s, err := scanMediaStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MediaStreamsBySources 批量取多个片源的轨道，按 source_id 分组。
// 列表响应靠它一次查出全部轨道，避免逐条片源各发一条 SQL。
func (d *DB) MediaStreamsBySources(sourceIDs []string) (map[string][]*MediaStream, error) {
	out := make(map[string][]*MediaStream, len(sourceIDs))
	const chunk = 400 // 分块，避免一条 SQL 里绑上千个变量
	for start := 0; start < len(sourceIDs); start += chunk {
		end := start + chunk
		if end > len(sourceIDs) {
			end = len(sourceIDs)
		}
		part := sourceIDs[start:end]
		marks := make([]string, len(part))
		args := make([]any, len(part))
		for i, id := range part {
			marks[i] = "?"
			args[i] = id
		}
		rows, err := d.Query(`
SELECT source_id, stream_index, type, codec, width, height, bit_rate, bit_depth, channels,
       frame_rate, language, title, video_range, profile, level, aspect_ratio, is_interlaced,
       color_primaries, color_transfer, color_space, pixel_format, sample_rate, channel_layout,
       is_default, is_forced
FROM media_streams WHERE source_id IN (`+strings.Join(marks, ",")+`)
ORDER BY source_id, stream_index`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			s, err := scanMediaStream(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[s.SourceID] = append(out[s.SourceID], s)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scanMediaStream 读一行 media_streams；列顺序与上面的 SELECT 保持一致。
func scanMediaStream(rows *sql.Rows) (*MediaStream, error) {
	var (
		s                       MediaStream
		interlaced, def, forced int
	)
	if err := rows.Scan(
		&s.SourceID, &s.Index, &s.Type, &s.Codec, &s.Width, &s.Height, &s.BitRate,
		&s.BitDepth, &s.Channels, &s.FrameRate, &s.Language, &s.Title, &s.VideoRange,
		&s.Profile, &s.Level, &s.AspectRatio, &interlaced, &s.ColorPrimaries,
		&s.ColorTransfer, &s.ColorSpace, &s.PixelFormat, &s.SampleRate, &s.ChannelLayout,
		&def, &forced,
	); err != nil {
		return nil, err
	}
	s.IsInterlaced = interlaced != 0
	s.IsDefault = def != 0
	s.IsForced = forced != 0
	return &s, nil
}

// SourceStub 是快速扫描用的片源摘要：只保留判断文件是否变化所需的字段。
type SourceStub struct {
	MTime int64
	Size  int64
}

// SourceSnapshot 返回某库已有片源的 strm_path -> mtime/size。
// 快速扫描用它跳过未变化的 .strm，避免重新解析和写库。
func (d *DB) SourceSnapshot(libraryID string) (map[string]SourceStub, error) {
	rows, err := d.Query(`
SELECT ms.strm_path, ms.strm_mtime, ms.strm_size
FROM media_sources ms
JOIN items i ON i.id = ms.item_id
WHERE i.library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]SourceStub, 4096)
	for rows.Next() {
		var path string
		var stub SourceStub
		if err := rows.Scan(&path, &stub.MTime, &stub.Size); err != nil {
			return nil, err
		}
		out[path] = stub
	}
	return out, rows.Err()
}

// SourcesForProbeWarmup 返回需要后台预热探测的片源。
//
//   - favoriteOnly=true：收藏的电影/单集，以及收藏剧集下的单集（最可能点开的内容）
//   - favoriteOnly=false 且 since>0：最近新增或有观看进度的片源
//   - favoriteOnly=false 且 since<=0：全库未探测片源（用于整库提取）
//
// 只挑 probe_status='none' 的，探测过的不会重复。
// mode: favorites（收藏）/ watching（收藏 + 在追）/ recent（最近新增）/ all（全库）。
func (d *DB) SourcesForProbeWarmup(since int64, limit int, mode string) ([]*MediaSource, error) {
	if limit <= 0 {
		limit = 50
	}

	where := `ms.probe_status = 'none' AND ms.url <> ''`
	args := make([]any, 0, 2)
	switch strings.ToLower(mode) {
	case "all":
		// 全库，不加过滤
	case "recent":
		if since > 0 {
			where += ` AND (i.created_at >= ?
      OR EXISTS (SELECT 1 FROM playstates p
                 WHERE p.item_id = i.id AND p.position_ticks > 0))`
			args = append(args, since)
		}
	default: // favorites / watching
		union, unionArgs := d.probeScopeUnion(mode)
		if union == "" {
			return []*MediaSource{}, nil
		}
		where += " AND ms.id IN (" + union + ")"
		args = append(args, unionArgs...)
	}

	q := `
SELECT ms.id, ms.item_id, ms.strm_path, ms.url, ms.cloud, ms.drive_id, ms.cloud_file_id, ms.sha1_etag,
       ms.size, ms.container, ms.file_name, ms.version_name, ms.sort_rank,
       ms.duration_ticks, ms.probe_status, ms.strm_mtime, ms.strm_size, ms.updated_at
FROM media_sources ms
JOIN items i ON i.id = ms.item_id
WHERE ` + where + `
ORDER BY i.created_at DESC
LIMIT ?`
	args = append(args, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*MediaSource, 0, limit)
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

// ProbePendingCounts 返回待探测片源总数，以及收藏、收藏+在追范围的数量。
func (d *DB) ProbePendingCounts() (total, favorites, watching int) {
	_ = d.QueryRow(
		`SELECT COUNT(*) FROM media_sources WHERE probe_status = 'none' AND url <> ''`,
	).Scan(&total)

	if u, args := d.probeScopeUnion("favorites"); u != "" {
		_ = d.QueryRow(`SELECT COUNT(*) FROM (`+u+`)`, args...).Scan(&favorites)
	}
	if u, args := d.probeScopeUnion("watching"); u != "" {
		_ = d.QueryRow(`SELECT COUNT(*) FROM (`+u+`)`, args...).Scan(&watching)
	}
	return total, favorites, watching
}

// favoriteIDs 返回收藏的条目 ID 和收藏的剧集 ID（收藏数据量很小）。
func (d *DB) favoriteIDs() (items, series []string, err error) {
	rows, err := d.Query(`
SELECT p.item_id, i.type FROM playstates p
JOIN items i ON i.id = p.item_id
WHERE p.is_favorite = 1`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, typ string
		if err := rows.Scan(&id, &typ); err != nil {
			return nil, nil, err
		}
		if typ == TypeSeries {
			series = append(series, id)
		} else {
			items = append(items, id)
		}
	}
	return items, series, rows.Err()
}

// activeSeriesIDs 返回最近在看的剧集：任一单集有未看完进度且最近更新过。
// 「一直在看一个剧」就把整部剧的剩余单集都加入预热。
func (d *DB) activeSeriesIDs(since int64) ([]string, error) {
	rows, err := d.Query(`
SELECT DISTINCT e.series_id FROM playstates p
JOIN items e ON e.id = p.item_id
WHERE e.type = 'Episode' AND e.series_id <> ''
  AND p.position_ticks > 0 AND p.updated_at >= ?`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// probeScopeUnion 生成「预热范围」的片源 ID UNION 子查询。
// 所有分支都从很小的收藏/在追集合出发走索引，避免扫描 29 万片源。
func (d *DB) probeScopeUnion(mode string) (string, []any) {
	items, series, err := d.favoriteIDs()
	var parts []string
	var args []any
	if err == nil {
		if len(items) > 0 {
			parts = append(parts, `SELECT ms.id FROM media_sources ms
WHERE ms.probe_status = 'none' AND ms.url <> ''
  AND ms.item_id IN (`+placeholders(len(items))+`)`)
			for _, id := range items {
				args = append(args, id)
			}
		}
		if len(series) > 0 {
			parts = append(parts, `SELECT ms.id FROM media_sources ms
JOIN items e ON e.id = ms.item_id
WHERE ms.probe_status = 'none' AND ms.url <> ''
  AND e.type = 'Episode' AND e.series_id IN (`+placeholders(len(series))+`)`)
			for _, id := range series {
				args = append(args, id)
			}
		}
	}

	if strings.EqualFold(mode, "watching") {
		active, aerr := d.activeSeriesIDs(time.Now().Add(-14 * 24 * time.Hour).Unix())
		if aerr == nil && len(active) > 0 {
			parts = append(parts, `SELECT ms.id FROM media_sources ms
JOIN items e ON e.id = ms.item_id
WHERE ms.probe_status = 'none' AND ms.url <> ''
  AND e.type = 'Episode' AND e.series_id IN (`+placeholders(len(active))+`)`)
			for _, id := range active {
				args = append(args, id)
			}
		}
	}

	return strings.Join(parts, " UNION "), args
}

// MediaSourcesForItemTree 返回条目本身及其子树（季/单集）的片源。
// 刷新剧集或季时，片源实际挂在单集上，需要一次性取回来。
func (d *DB) MediaSourcesForItemTree(itemID string) ([]*MediaSource, error) {
	rows, err := d.Query(`
SELECT ms.id, ms.item_id, ms.strm_path, ms.url, ms.cloud, ms.drive_id, ms.cloud_file_id, ms.sha1_etag,
       ms.size, ms.container, ms.file_name, ms.version_name, ms.sort_rank,
       ms.duration_ticks, ms.probe_status, ms.strm_mtime, ms.strm_size, ms.updated_at
FROM media_sources ms
JOIN items i ON i.id = ms.item_id
WHERE i.id = ? OR i.series_id = ? OR i.parent_id = ?
ORDER BY ms.sort_rank, ms.size DESC, ms.id`, itemID, itemID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*MediaSource, 0, 4)
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
