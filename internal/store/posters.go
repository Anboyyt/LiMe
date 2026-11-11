package store

import (
	"path"
	"strings"
)

// PosterItem 是海报回填需要的最小条目信息。
type PosterItem struct {
	ID        string
	Type      string
	TmdbID    string
	ImagePath string // 当前本地相对路径，批量替换时按它更新同组条目
}

// PosterBackfillItems 返回带 TMDB ID、且海报来自 TMDB 的电影和剧集。
// 面板手工上传的封面（文件名形如 <itemID>-primary-<时间戳>.jpg）要跳过，
// 否则回填会把用户自己换的图覆盖掉。
func (d *DB) PosterBackfillItems() ([]PosterItem, error) {
	rows, err := d.Query(
		`SELECT id, type, tmdb_id, image_path
		   FROM items
		  WHERE type IN (?, ?) AND tmdb_id <> '' AND tmdb_id IS NOT NULL
		    AND image_path <> ''
		  ORDER BY CASE type WHEN ? THEN 0 ELSE 1 END, id`,
		TypeMovie, TypeSeries, TypeSeries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PosterItem
	for rows.Next() {
		var it PosterItem
		if err := rows.Scan(&it.ID, &it.Type, &it.TmdbID, &it.ImagePath); err != nil {
			return nil, err
		}
		if !TMDBImagePath(it.ImagePath) {
			continue
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// TMDBImagePath 判断相对路径是否是刮削下载的图：images/<24 位十六进制>.<ext>。
// 面板上传的图、动图封面都不符合这个形状。
func TMDBImagePath(rel string) bool {
	if !strings.HasPrefix(rel, "images/") {
		return false
	}
	base := path.Base(rel)
	ext := path.Ext(base)
	if ext != ".jpg" && ext != ".png" {
		return false
	}
	name := strings.TrimSuffix(base, ext)
	if len(name) != 24 {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ReplacePoster 把同组条目的海报一起换掉：季/集通常共享剧集海报，
// 只改主条目会让子条目继续指向旧文件。
func (d *DB) ReplacePoster(oldPath, newPath string) error {
	_, err := d.Exec(`UPDATE items SET image_path = ? WHERE image_path = ?`, newPath, oldPath)
	return err
}

// CountPosterRefs 统计还有多少条目引用这张海报（用于安全删除旧文件）。
func (d *DB) CountPosterRefs(rel string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM items WHERE image_path = ?`, rel).Scan(&n)
	return n, err
}
