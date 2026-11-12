package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
)

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
