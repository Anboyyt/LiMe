package store

import "lime/internal/pinyin"

// BackfillPinyin 给迁移前已入库的条目补拼音排序键。
// 按主键分页扫描，整个过程只过一遍表；已补过的行由 sort_key = ” 过滤掉。
// 返回补全的行数。
func (d *DB) BackfillPinyin() (int, error) {
	// 季/集不需要拼音，排序键直接沿用排序名，一条 SQL 批量处理。
	res, err := d.Exec(`UPDATE items SET sort_key = sort_name
		WHERE sort_key = '' AND type IN ('Season', 'Episode')`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	total := int(n)

	const batch = 5000
	lastID := ""
	for {
		type row struct{ id, name, sortName string }
		rows, err := d.Query(`SELECT id, name, sort_name FROM items
			WHERE id > ? AND sort_key = '' AND type IN ('Movie', 'Series')
			ORDER BY id LIMIT ?`, lastID, batch)
		if err != nil {
			return total, err
		}
		var items []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.name, &r.sortName); err != nil {
				rows.Close()
				return total, err
			}
			items = append(items, r)
		}
		rows.Close()
		if len(items) == 0 {
			break
		}
		lastID = items[len(items)-1].id

		tx, err := d.Begin()
		if err != nil {
			return total, err
		}
		stmt, err := tx.Prepare(`UPDATE items SET sort_key = ?, pinyin = ?, pinyin_abbr = ? WHERE id = ?`)
		if err != nil {
			_ = tx.Rollback()
			return total, err
		}
		for _, r := range items {
			full, abbr := pinyin.Key(r.name)
			key := full
			if key == "" {
				key = r.sortName
			}
			if key == "" {
				key = r.id // 名字和排序名都空的脏数据，至少别留空键
			}
			if _, err := stmt.Exec(key, full, abbr, r.id); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return total, err
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			return total, err
		}
		total += len(items)
	}
	return total, nil
}
