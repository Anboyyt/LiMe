package store

import "strings"

const itemPersonsBackfillKey = "item_persons_backfill"

// EnsureItemPersonsIndex 创建维护 item_persons 的触发器。
// 迁移文件按分号切分，BEGIN...END 里的分号会被切坏，所以触发器必须单独 Exec。
func (d *DB) EnsureItemPersonsIndex() error {
	stmts := []string{
		`CREATE TRIGGER IF NOT EXISTS item_persons_ai AFTER INSERT ON items
WHEN new.people <> ''
BEGIN
    INSERT OR IGNORE INTO item_persons (item_id, name, name_lower)
    SELECT new.id,
           TRIM(json_extract(p.value, '$.Name')),
           LOWER(TRIM(json_extract(p.value, '$.Name')))
    FROM json_each(CASE WHEN json_valid(new.people) THEN new.people ELSE '[]' END) p
    WHERE TRIM(COALESCE(json_extract(p.value, '$.Name'), '')) <> '';
END`,
		`CREATE TRIGGER IF NOT EXISTS item_persons_au AFTER UPDATE OF people ON items
WHEN old.people IS NOT new.people
BEGIN
    DELETE FROM item_persons WHERE item_id = old.id;
    INSERT OR IGNORE INTO item_persons (item_id, name, name_lower)
    SELECT new.id,
           TRIM(json_extract(p.value, '$.Name')),
           LOWER(TRIM(json_extract(p.value, '$.Name')))
    FROM json_each(CASE WHEN json_valid(new.people) THEN new.people ELSE '[]' END) p
    WHERE TRIM(COALESCE(json_extract(p.value, '$.Name'), '')) <> '';
END`,
		`CREATE TRIGGER IF NOT EXISTS item_persons_ad AFTER DELETE ON items
BEGIN
    DELETE FROM item_persons WHERE item_id = old.id;
END`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// BackfillItemPersons 把已有条目的 people JSON 展开进 item_persons。
// 迁移只建表，不阻塞启动；这里用 keyset 分批，避免一次性把几十万行读进内存。
func (d *DB) BackfillItemPersons() (int, error) {
	if v, err := d.GetSetting(itemPersonsBackfillKey); err == nil && v == "done" {
		return 0, nil
	}

	total := 0
	lastID := ""
	for {
		ids, err := d.itemIDsWithPeopleAfter(lastID, 400)
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
INSERT OR IGNORE INTO item_persons (item_id, name, name_lower)
SELECT i.id,
       TRIM(json_extract(p.value, '$.Name')),
       LOWER(TRIM(json_extract(p.value, '$.Name')))
FROM items i, json_each(CASE WHEN json_valid(i.people) THEN i.people ELSE '[]' END) p
WHERE i.id IN (`+placeholders(len(ids))+`)
  AND TRIM(COALESCE(json_extract(p.value, '$.Name'), '')) <> ''`, args...)
		if err != nil {
			return total, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += int(n)
		}
	}
	if err := d.SetSetting(itemPersonsBackfillKey, "done"); err != nil {
		return total, err
	}
	return total, nil
}

func (d *DB) itemIDsWithPeopleAfter(after string, limit int) ([]string, error) {
	rows, err := d.Query(`
SELECT id FROM items
WHERE people <> '' AND id > ?
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

// lowerPersonNames 给查询参数统一小写，和 item_persons.name_lower 对齐。
func lowerPersonNames(names []string) []any {
	out := make([]any, 0, len(names))
	for _, n := range names {
		if v := strings.ToLower(strings.TrimSpace(n)); v != "" {
			out = append(out, v)
		}
	}
	return out
}
