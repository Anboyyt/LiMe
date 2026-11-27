package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"lime/internal/strm"
)

// ReassignOrphanPlaystates 把「已经没有片源、但还留着播放记录」的电影
// 条目的进度迁到同番号的新条目上。归并键从目录改成番号后，旧条目会
// 变成空壳；不迁移的话，已看和续播会跟着丢。
func (d *DB) ReassignOrphanPlaystates() (int64, error) {
	rows, err := d.Query(`
SELECT i.id, i.name FROM items i
WHERE i.type = 'Movie'
  AND EXISTS (SELECT 1 FROM playstates p WHERE p.item_id = i.id)
  AND NOT EXISTS (SELECT 1 FROM media_sources ms WHERE ms.item_id = i.id)`)
	if err != nil {
		return 0, err
	}
	type orphan struct{ id, name string }
	var list []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.name); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var moved int64
	for _, o := range list {
		code := strm.Code(o.name)
		if code == "" {
			continue
		}
		var target string
		err := d.QueryRow(`
SELECT i.id FROM items i
WHERE i.type = 'Movie' AND i.id <> ? AND i.name LIKE ?
  AND EXISTS (SELECT 1 FROM media_sources ms WHERE ms.item_id = i.id)
LIMIT 1`, o.id, code+"%").Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return moved, err
		}
		if err := d.movePlaystates(o.id, target); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

func (d *DB) movePlaystates(from, to string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO playstates (user_id, item_id, position_ticks, played, is_favorite, updated_at)
SELECT user_id, ?, position_ticks, played, is_favorite, updated_at
FROM playstates WHERE item_id = ?
ON CONFLICT(user_id, item_id) DO UPDATE SET
  played = MAX(playstates.played, excluded.played),
  position_ticks = CASE
    WHEN excluded.played = 1 THEN 0
    WHEN excluded.position_ticks > playstates.position_ticks THEN excluded.position_ticks
    ELSE playstates.position_ticks END,
  is_favorite = MAX(playstates.is_favorite, excluded.is_favorite),
  updated_at = MAX(playstates.updated_at, excluded.updated_at)`, to, from); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM playstates WHERE item_id = ?`, from); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CountSources 返回库内片源数量，用于扫描进度估算，避免扫描前再遍历一遍目录。
func (d *DB) CountSources(libraryID string) (int, error) {
	var n int
	err := d.QueryRow(`
SELECT COUNT(*) FROM media_sources ms
JOIN items i ON i.id = ms.item_id
WHERE i.library_id = ?`, libraryID).Scan(&n)
	return n, err
}

// SourcePathsUnder 返回 strm_path 位于 prefix 目录下的片源路径。
// Linux 上用索引范围查询；Windows 测试路径是反斜杠，退回字符串匹配。
func (d *DB) SourcePathsUnder(prefix string) ([]string, error) {
	prefix = strings.TrimRight(filepath.ToSlash(prefix), "/") + "/"
	var (
		rows *sql.Rows
		err  error
	)
	if filepath.Separator == '\\' {
		rows, err = d.Query(`
SELECT strm_path FROM media_sources
WHERE instr(replace(strm_path, '\', '/'), ?) = 1`, prefix)
	} else {
		// 前缀以 "/" 结尾，把 "/" 递增成 "0" 得到范围上界，能走 idx_media_sources_strm。
		upper := prefix[:len(prefix)-1] + "0"
		rows, err = d.Query(`
SELECT strm_path FROM media_sources
WHERE strm_path >= ? AND strm_path < ?`, prefix, upper)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteSourceByPath 删除指定 .strm 对应的片源，返回所属条目 ID。
// 路径不存在时返回空 ID 和 nil。
func (d *DB) DeleteSourceByPath(path string) (string, error) {
	var itemID string
	err := d.QueryRow(`SELECT item_id FROM media_sources WHERE strm_path = ? LIMIT 1`, path).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := d.Exec(`DELETE FROM media_sources WHERE strm_path = ?`, path); err != nil {
		return "", err
	}
	return itemID, nil
}

// CountSourcesForItem 返回条目还剩几个片源。
func (d *DB) CountSourcesForItem(itemID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM media_sources WHERE item_id = ?`, itemID).Scan(&n)
	return n, err
}

// DeleteItem 删除条目；片源、流信息、播放状态由外键级联清理。
func (d *DB) DeleteItem(id string) error {
	_, err := d.Exec(`DELETE FROM items WHERE id = ?`, id)
	return err
}

// CountItemsByParent 返回直接子条目数量（季下的集数）。
func (d *DB) CountItemsByParent(parentID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM items WHERE parent_id = ?`, parentID).Scan(&n)
	return n, err
}

// CountEpisodesBySeries 返回剧集下剩余的单集数量。
func (d *DB) CountEpisodesBySeries(seriesID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM items WHERE series_id = ? AND type = ?`, seriesID, TypeEpisode).Scan(&n)
	return n, err
}

// DeleteSeriesTree 删除剧集及其所有季/集条目；只应在没有单集时调用。
func (d *DB) DeleteSeriesTree(seriesID string) error {
	_, err := d.Exec(`DELETE FROM items WHERE id = ? OR series_id = ?`, seriesID, seriesID)
	return err
}

// StaleItemsWithSources 返回库内 updated_at < before 的条目及其片源路径。
// 全量扫描用它做安全清理：调用方确认所有路径都不存在后才删除条目，
// 避免文件临时不可读时误删条目和播放进度。
func (d *DB) StaleItemsWithSources(libraryID string, before int64) (map[string][]string, error) {
	rows, err := d.Query(`
SELECT i.id, COALESCE(ms.strm_path, '')
FROM items i
LEFT JOIN media_sources ms ON ms.item_id = i.id
WHERE i.library_id = ? AND i.updated_at < ?
GROUP BY i.id, ms.strm_path`, libraryID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string)
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		paths, ok := out[id]
		if !ok {
			paths = []string{}
		}
		if path != "" {
			paths = append(paths, path)
		}
		out[id] = paths
	}
	return out, rows.Err()
}

// PurgeOrphanItems 清理没有任何片源的电影/单集，以及因此变空的季和剧。
// 正常删除由实时监控/扫描负责；这里兜底历史版本留下的空壳。
// 片源表为空时直接跳过，避免挂载异常时把整库清空。
func (d *DB) PurgeOrphanItems() (int, error) {
	var sources int
	if err := d.QueryRow(`SELECT COUNT(*) FROM media_sources`).Scan(&sources); err != nil {
		return 0, err
	}
	if sources == 0 {
		return 0, nil
	}

	steps := []string{
		`DELETE FROM items WHERE type IN ('Movie', 'Episode')
		 AND NOT EXISTS (SELECT 1 FROM media_sources ms WHERE ms.item_id = items.id)`,
		`DELETE FROM items WHERE type = 'Season'
		 AND NOT EXISTS (SELECT 1 FROM items e WHERE e.parent_id = items.id)`,
		`DELETE FROM items WHERE type = 'Series'
		 AND NOT EXISTS (SELECT 1 FROM items e WHERE e.series_id = items.id)`,
	}
	total := 0
	for _, q := range steps {
		res, err := d.Exec(q)
		if err != nil {
			return total, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += int(n)
		}
	}
	return total, nil
}
