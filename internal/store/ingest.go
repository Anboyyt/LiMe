package store

import "time"

// PendingIngest 是一条刚写入、还没发过入库通知的片源。
// 只取通知需要的字段，条目的标题/演员/海报由调用方用 ItemsByIDs 批量补。
type PendingIngest struct {
	ID          string
	ItemID      string
	StrmPath    string
	FileName    string
	VersionName string
	Cloud       string
	Size        int64
	IngestedAt  int64
}

// CountPendingIngest 统计在 cutoff 之前入库、还没通知的片源数量。
func (d *DB) CountPendingIngest(cutoff int64) (int, error) {
	var n int
	err := d.QueryRow(`
SELECT COUNT(*) FROM media_sources
WHERE notified_at = 0 AND ingested_at > 0 AND ingested_at <= ?`, cutoff).Scan(&n)
	return n, err
}

// PendingIngestSources 返回最多 limit 条待通知片源，按入库时间正序，
// 让同一批入库的多个文件尽量落在同一次分组里。
func (d *DB) PendingIngestSources(cutoff int64, limit int) ([]PendingIngest, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := d.Query(`
SELECT id, item_id, strm_path, file_name, version_name, cloud, size, ingested_at
FROM media_sources
WHERE notified_at = 0 AND ingested_at > 0 AND ingested_at <= ?
ORDER BY ingested_at ASC, id ASC
LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PendingIngest, 0, limit)
	for rows.Next() {
		var p PendingIngest
		if err := rows.Scan(
			&p.ID, &p.ItemID, &p.StrmPath,
			&p.FileName, &p.VersionName, &p.Cloud, &p.Size, &p.IngestedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkIngestNotified 把一批片源标记成已通知。按 500 个一组更新，
// 避免超过 SQLite 的变量上限。
func (d *DB) MarkIngestNotified(ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	ts := at.Unix()
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		args := make([]any, 0, len(part)+1)
		args = append(args, ts)
		for _, id := range part {
			args = append(args, id)
		}
		if _, err := d.Exec(
			`UPDATE media_sources SET notified_at = ? WHERE id IN (`+
				placeholders(len(part))+`)`,
			args...,
		); err != nil {
			return err
		}
	}
	return nil
}

// MarkAllPendingIngestNotified 用于关闭入库通知或大批量扫描时清空队列，
// 避免下次打开通知后把历史积压一次刷出来。
func (d *DB) MarkAllPendingIngestNotified(cutoff int64, at time.Time) (int64, error) {
	res, err := d.Exec(`
UPDATE media_sources SET notified_at = ?
WHERE notified_at = 0 AND ingested_at > 0 AND ingested_at <= ?`, at.Unix(), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
