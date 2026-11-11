package store

import (
	"database/sql"
	"errors"
)

// NextUp 返回每个看过的剧集「接下来该看的一集」，按最近播放倒序。
// 已看到一半的集优先返回该集本身；上一集看完则返回下一集。
func (d *DB) NextUp(userID string, limit, start int, seriesID string) ([]*Item, int, error) {
	if userID == "" {
		return []*Item{}, 0, nil
	}

	query := `
SELECT i.series_id, MAX(p.updated_at)
FROM playstates p
JOIN items i ON i.id = p.item_id
WHERE p.user_id = ?
  AND i.type = 'Episode'
  AND i.series_id <> ''
  AND (p.played = 1 OR p.position_ticks > 0)`
	args := []any{userID}
	if seriesID != "" {
		query += " AND i.series_id = ?"
		args = append(args, seriesID)
	}
	query += " GROUP BY i.series_id ORDER BY MAX(p.updated_at) DESC"

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}

	// 先把剧集列表读完再逐部查下一集：连接只有一条，
	// 在 rows 迭代中发新查询会自己把自己锁死。
	var seriesIDs []string
	for rows.Next() {
		var sid string
		var ts int64
		if err := rows.Scan(&sid, &ts); err != nil {
			rows.Close()
			return nil, 0, err
		}
		seriesIDs = append(seriesIDs, sid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}

	var ids []string
	for _, sid := range seriesIDs {
		it, err := d.nextEpisodeForSeries(userID, sid)
		if err != nil {
			return nil, 0, err
		}
		if it != nil {
			ids = append(ids, it.ID)
		}
	}

	total := len(ids)
	if start < 0 {
		start = 0
	}
	if start >= total {
		return []*Item{}, total, nil
	}
	if limit <= 0 {
		limit = 25
	}
	end := start + limit
	if end > total {
		end = total
	}

	page := ids[start:end]
	byID, err := d.ItemsByIDs(page)
	if err != nil {
		return nil, 0, err
	}
	items := make([]*Item, 0, len(page))
	for _, id := range page {
		if it, ok := byID[id]; ok {
			items = append(items, it)
		}
	}
	return items, total, nil
}

// nextEpisodeForSeries 找一部剧的下一集；没有可看的返回 nil。
func (d *DB) nextEpisodeForSeries(userID, seriesID string) (*Item, error) {
	var (
		id     string
		ord    int64
		played int
	)
	err := d.QueryRow(`
SELECT i.id,
       i.parent_index_number * 10000 + i.index_number,
       COALESCE(p.played, 0)
FROM items i
JOIN playstates p ON p.user_id = ? AND p.item_id = i.id
WHERE i.type = 'Episode' AND i.series_id = ?
  AND (p.played = 1 OR p.position_ticks > 0)
ORDER BY i.parent_index_number DESC, i.index_number DESC
LIMIT 1`, userID, seriesID).Scan(&id, &ord, &played)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 上一集看到一半：继续这一集
	if played == 0 {
		return d.GetItem(id)
	}

	// 上一集看完了：找它之后第一个没看过的
	row := d.QueryRow(`
SELECT `+itemColumns+` FROM items
WHERE type = 'Episode'
  AND series_id = ?
  AND parent_index_number * 10000 + index_number > ?
  AND NOT EXISTS (
    SELECT 1 FROM playstates p
    WHERE p.user_id = ? AND p.item_id = items.id AND p.played = 1
  )
ORDER BY parent_index_number, index_number
LIMIT 1`, seriesID, ord, userID)

	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return it, err
}
