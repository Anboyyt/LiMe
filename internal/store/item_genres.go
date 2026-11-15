package store

const itemGenresBackfillKey = "item_genres_backfill"

// EnsureItemGenresIndex 创建 items.genres -> item_genres 的同步触发器。
func (d *DB) EnsureItemGenresIndex() error {
	stmts := []string{
		`CREATE TRIGGER IF NOT EXISTS item_genres_ai AFTER INSERT ON items
WHEN new.genres <> ''
BEGIN
    INSERT OR IGNORE INTO item_genres (item_id, genre)
    SELECT new.id, TRIM(CAST(g.value AS TEXT))
    FROM json_each(CASE WHEN json_valid(new.genres) THEN new.genres ELSE '[]' END) g
    WHERE TRIM(COALESCE(CAST(g.value AS TEXT), '')) <> '';
END`,
		`CREATE TRIGGER IF NOT EXISTS item_genres_au AFTER UPDATE OF genres ON items
WHEN old.genres IS NOT new.genres
BEGIN
    DELETE FROM item_genres WHERE item_id = old.id;
    INSERT OR IGNORE INTO item_genres (item_id, genre)
    SELECT new.id, TRIM(CAST(g.value AS TEXT))
    FROM json_each(CASE WHEN json_valid(new.genres) THEN new.genres ELSE '[]' END) g
    WHERE TRIM(COALESCE(CAST(g.value AS TEXT), '')) <> '';
END`,
		`CREATE TRIGGER IF NOT EXISTS item_genres_ad AFTER DELETE ON items
BEGIN
    DELETE FROM item_genres WHERE item_id = old.id;
END`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// BackfillItemGenres 把已有条目的 genres JSON 展开进 item_genres。
func (d *DB) BackfillItemGenres() (int, error) {
	if v, err := d.GetSetting(itemGenresBackfillKey); err == nil && v == "done" {
		return 0, nil
	}
	total := 0
	lastID := ""
	for {
		ids, err := d.itemIDsWithGenresAfter(lastID, 400)
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		lastID = ids[len(ids)-1]
		args := make([]any, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
		}
		res, err := d.Exec(`
INSERT OR IGNORE INTO item_genres (item_id, genre)
SELECT i.id, TRIM(CAST(g.value AS TEXT))
FROM items i, json_each(CASE WHEN json_valid(i.genres) THEN i.genres ELSE '[]' END) g
WHERE i.id IN (`+placeholders(len(ids))+`)
  AND TRIM(COALESCE(CAST(g.value AS TEXT), '')) <> ''`, args...)
		if err != nil {
			return total, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += int(n)
		}
	}
	if err := d.SetSetting(itemGenresBackfillKey, "done"); err != nil {
		return total, err
	}
	return total, nil
}

func (d *DB) itemIDsWithGenresAfter(after string, limit int) ([]string, error) {
	rows, err := d.Query(`
SELECT id FROM items
WHERE genres <> '' AND id > ?
ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
