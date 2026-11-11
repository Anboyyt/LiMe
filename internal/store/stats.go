package store

type LibraryStat struct {
	ID    string
	Name  string
	Path  string
	Paths []string
	Kind  string
	Items int64
}

// Stats 是运维面板用的全局统计。
type Stats struct {
	Libraries    []LibraryStat
	ItemsByType  map[string]int64
	TotalItems   int64
	MediaSources int64
	Playstates   int64
	Users        int64
}

// ProductionYears 返回全部库里有条目的年份。
func (d *DB) ProductionYears() ([]int, error) { return d.ProductionYearsIn(nil) }

// ProductionYearsIn 只统计指定库的年份；libraryIDs 为空表示全部。
func (d *DB) ProductionYearsIn(libraryIDs []string) ([]int, error) {
	q := `SELECT DISTINCT production_year FROM items WHERE production_year > 0`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		q += ` AND library_id IN (` + placeholders(len(libraryIDs)) + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY production_year`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]int, 0, 32)
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// CountItemsByType 统计全部库的条目类型。
func (d *DB) CountItemsByType() (map[string]int64, error) { return d.CountItemsByTypeIn(nil) }

// CountItemsByTypeIn 只统计指定库；libraryIDs 为空表示全部。
func (d *DB) CountItemsByTypeIn(libraryIDs []string) (map[string]int64, error) {
	q := `SELECT type, COUNT(*) FROM items`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		q += ` WHERE library_id IN (` + placeholders(len(libraryIDs)) + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	q += ` GROUP BY type`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, 4)
	for rows.Next() {
		var (
			typ string
			n   int64
		)
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, err
		}
		out[typ] = n
	}
	return out, rows.Err()
}

func (d *DB) Stats() (*Stats, error) {
	s := &Stats{
		Libraries:   make([]LibraryStat, 0, 8),
		ItemsByType: make(map[string]int64, 4),
	}

	// 相关子查询走 idx_items_library_type_sort 的 library_id 前缀，
	// 比 LEFT JOIN + GROUP BY 扫全表快一个数量级（26 万行：553ms -> 47ms）。
	rows, err := d.Query(`
SELECT l.id, l.name, l.path, l.kind,
       (SELECT COUNT(*) FROM items i WHERE i.library_id = l.id)
FROM libraries l
ORDER BY l.sort_order, l.name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l LibraryStat
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &l.Kind, &l.Items); err != nil {
			rows.Close()
			return nil, err
		}
		s.Libraries = append(s.Libraries, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(s.Libraries))
	for _, l := range s.Libraries {
		ids = append(ids, l.ID)
	}
	paths, err := d.pathsForLibraries(ids)
	if err != nil {
		return nil, err
	}
	for i := range s.Libraries {
		s.Libraries[i].Paths = paths[s.Libraries[i].ID]
		if len(s.Libraries[i].Paths) == 0 && s.Libraries[i].Path != "" {
			s.Libraries[i].Paths = []string{s.Libraries[i].Path}
		}
	}

	rows, err = d.Query(`SELECT type, COUNT(*) FROM items GROUP BY type`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			typ string
			n   int64
		)
		if err := rows.Scan(&typ, &n); err != nil {
			rows.Close()
			return nil, err
		}
		s.ItemsByType[typ] = n
		s.TotalItems += n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	counts := []struct {
		query string
		dst   *int64
	}{
		{`SELECT COUNT(*) FROM media_sources`, &s.MediaSources},
		{`SELECT COUNT(*) FROM playstates`, &s.Playstates},
		{`SELECT COUNT(*) FROM users`, &s.Users},
	}
	for _, c := range counts {
		if err := d.QueryRow(c.query).Scan(c.dst); err != nil {
			return nil, err
		}
	}
	return s, nil
}
