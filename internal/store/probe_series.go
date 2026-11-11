package store

import "time"

// SourcesForSeriesWarm 返回某部剧里还没探测过的单集片源，按季/集排序。
// 用于播放进度上报后立即预热整部剧，让下一集起播不用等 ffprobe。
func (d *DB) SourcesForSeriesWarm(seriesID string, limit int) ([]*MediaSource, error) {
	if seriesID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	rows, err := d.Query(`
SELECT ms.id, ms.item_id, ms.strm_path, ms.url, ms.cloud, ms.drive_id, ms.cloud_file_id, ms.sha1_etag,
       ms.size, ms.container, ms.file_name, ms.version_name, ms.sort_rank,
       ms.duration_ticks, ms.probe_status, ms.strm_mtime, ms.strm_size, ms.updated_at
FROM media_sources ms
JOIN items e ON e.id = ms.item_id
WHERE ms.probe_status = 'none' AND ms.url <> ''
  AND e.type = 'Episode' AND e.series_id = ?
ORDER BY e.parent_index_number, e.index_number
LIMIT ?`, seriesID, limit)
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
