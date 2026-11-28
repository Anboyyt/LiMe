package store

// AttentionItem 是「待处理」列表里的一行，只带面板需要的最小字段。
type AttentionItem struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	Type            string `json:"Type"`
	TmdbID          string `json:"TmdbId"`
	Path            string `json:"Path"`
	ProductionYear  int    `json:"ProductionYear,omitempty"`
	MissingPoster   bool   `json:"MissingPoster,omitempty"`
	MissingBackdrop bool   `json:"MissingBackdrop,omitempty"`
}

// DuplicateGroup 是同一个 TMDB ID 出现在多个条目里的分组。
type DuplicateGroup struct {
	TmdbID string          `json:"TmdbId"`
	Type   string          `json:"Type"`
	Items  []AttentionItem `json:"Items"`
}

const attentionColumns = `
	i.id, i.name, i.type, i.tmdb_id,
	COALESCE((SELECT s.strm_path FROM media_sources s WHERE s.item_id = i.id LIMIT 1), ''),
	i.production_year, i.image_path, i.backdrop_path`

func scanAttentionItem(row interface {
	Scan(dest ...any) error
}) (AttentionItem, error) {
	var it AttentionItem
	var imagePath, backdropPath string
	err := row.Scan(&it.ID, &it.Name, &it.Type, &it.TmdbID, &it.Path,
		&it.ProductionYear, &imagePath, &backdropPath)
	if err != nil {
		return it, err
	}
	it.MissingPoster = imagePath == ""
	it.MissingBackdrop = backdropPath == ""
	return it, nil
}

// ItemsMissingTmdbID 返回没有 TMDB ID 的电影/剧集。
func (d *DB) ItemsMissingTmdbID(limit int) ([]AttentionItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.Query(`
SELECT `+attentionColumns+`
FROM items i
WHERE i.type IN (?, ?) AND (i.tmdb_id IS NULL OR i.tmdb_id = '')
ORDER BY i.sort_name, i.id
LIMIT ?`, TypeMovie, TypeSeries, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AttentionItem, 0, 16)
	for rows.Next() {
		it, err := scanAttentionItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsMissingArtwork 返回缺海报或缺背景图的电影/剧集。
func (d *DB) ItemsMissingArtwork(limit int) ([]AttentionItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.Query(`
SELECT `+attentionColumns+`
FROM items i
WHERE i.type IN (?, ?)
  AND (i.image_path = '' OR i.image_path IS NULL
       OR i.backdrop_path = '' OR i.backdrop_path IS NULL)
ORDER BY i.sort_name, i.id
LIMIT ?`, TypeMovie, TypeSeries, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AttentionItem, 0, 16)
	for rows.Next() {
		it, err := scanAttentionItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DuplicateTmdbGroups 找出同一个 TMDB ID 对应多个条目的情况。
// 多版本目录在入库时会合并成一个条目，所以这里通常是真正的重复或误识别。
func (d *DB) DuplicateTmdbGroups(limit int) ([]DuplicateGroup, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Query(`
SELECT tmdb_id, type, COUNT(*) AS n
FROM items
WHERE tmdb_id IS NOT NULL AND tmdb_id <> '' AND type IN (?, ?)
GROUP BY tmdb_id, type
HAVING COUNT(*) > 1
ORDER BY n DESC, tmdb_id
LIMIT ?`, TypeMovie, TypeSeries, limit)
	if err != nil {
		return nil, err
	}
	type key struct{ id, typ string }
	keys := make([]key, 0, 16)
	for rows.Next() {
		var k key
		var n int
		if err := rows.Scan(&k.id, &k.typ, &n); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	out := make([]DuplicateGroup, 0, len(keys))
	for _, k := range keys {
		items, err := d.itemsByTmdb(k.id, k.typ, 50)
		if err != nil {
			return nil, err
		}
		out = append(out, DuplicateGroup{TmdbID: k.id, Type: k.typ, Items: items})
	}
	return out, nil
}

func (d *DB) itemsByTmdb(tmdbID, typ string, limit int) ([]AttentionItem, error) {
	rows, err := d.Query(`
SELECT `+attentionColumns+`
FROM items i
WHERE i.tmdb_id = ? AND i.type = ?
ORDER BY i.library_id, i.sort_name
LIMIT ?`, tmdbID, typ, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AttentionItem, 0, 4)
	for rows.Next() {
		it, err := scanAttentionItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DuplicateTmdbCount 只数重复组数量，首页计数用，不展开每组条目。
func (d *DB) DuplicateTmdbCount() (int, error) {
	var n int
	err := d.QueryRow(`
SELECT COUNT(*) FROM (
  SELECT tmdb_id, type FROM items
  WHERE tmdb_id IS NOT NULL AND tmdb_id <> '' AND type IN (?, ?)
  GROUP BY tmdb_id, type HAVING COUNT(*) > 1
)`, TypeMovie, TypeSeries).Scan(&n)
	return n, err
}
