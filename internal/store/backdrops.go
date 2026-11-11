package store

// BackdropItem 是背景图回填需要的最小条目信息。
type BackdropItem struct {
	ID           string
	Type         string
	TmdbID       string
	BackdropPath string // 当前本地相对路径，批量替换时按它更新同组条目
}

// BackdropBackfillItems 返回带 TMDB ID 的电影和剧集。
// 季/集共用所属剧集的背景图，不需要单独请求 TMDB。
func (d *DB) BackdropBackfillItems() ([]BackdropItem, error) {
	// 剧集详情页最常用大背景图，先回填剧集，再处理电影。
	rows, err := d.Query(
		`SELECT id, type, tmdb_id, backdrop_path
		   FROM items
		  WHERE type IN (?, ?) AND tmdb_id <> '' AND tmdb_id IS NOT NULL
		  ORDER BY CASE type WHEN ? THEN 0 ELSE 1 END, id`,
		TypeMovie, TypeSeries, TypeSeries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BackdropItem
	for rows.Next() {
		var it BackdropItem
		if err := rows.Scan(&it.ID, &it.Type, &it.TmdbID, &it.BackdropPath); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ReplaceBackdrop 把同组条目的背景图一起换掉。
// 剧集、季、集通常共享同一个背景图文件，只改主演条目会让子条目继续指向旧文件。
func (d *DB) ReplaceBackdrop(itemID, oldPath, newPath string) error {
	if oldPath == "" {
		_, err := d.Exec(`UPDATE items SET backdrop_path = ? WHERE id = ?`, newPath, itemID)
		return err
	}
	_, err := d.Exec(`UPDATE items SET backdrop_path = ? WHERE backdrop_path = ?`, newPath, oldPath)
	return err
}

// CountBackdropRefs 统计还有多少条目引用这个背景图文件（用于安全删除旧文件）。
func (d *DB) CountBackdropRefs(rel string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM items WHERE backdrop_path = ?`, rel).Scan(&n)
	return n, err
}
