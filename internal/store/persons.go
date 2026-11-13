package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type PersonImage struct {
	ID       string
	Name     string
	ImageURL string
}

// personsBackfillSetting 标记 items.people 已经展开进 persons 表。
const personsBackfillSetting = "persons_backfill_v1"

// PersonID 用演员名生成稳定的 32 位十六进制 ID，和 Jellyfin 的 ID 形态一致。
// 注意：这是单向哈希，无法从 ID 反推名字，所以 persons 表必须保留 name。
func PersonID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	sum := sha256.Sum256([]byte("person:" + name))
	return hex.EncodeToString(sum[:16])
}

// UpsertPersonImages 批量记录演员头像地址；同一演员只保留一条。
func (d *DB) UpsertPersonImages(people []PersonImage) error {
	return d.upsertPersons(people, true)
}

// UpsertPersons 记录演职人员，允许没有头像。
func (d *DB) UpsertPersons(people []PersonImage) error {
	return d.upsertPersons(people, false)
}

func (d *DB) upsertPersons(people []PersonImage, requireImage bool) error {
	if len(people) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	for _, p := range people {
		if p.ID == "" || p.Name == "" {
			continue
		}
		if requireImage && p.ImageURL == "" {
			continue
		}
		if err := upsertPerson(tx, p, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func upsertPerson(tx execer, p PersonImage, now int64) error {
	// 空头像不覆盖已有头像：刮削流程可能先写入图片地址，再补全无头像的演员。
	_, err := tx.Exec(`
INSERT INTO persons (id, name, image_url, updated_at) VALUES (?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE persons.name END,
  image_url = CASE WHEN excluded.image_url <> '' THEN excluded.image_url ELSE persons.image_url END,
  updated_at = excluded.updated_at`,
		p.ID, p.Name, p.ImageURL, now,
	)
	return err
}

func (d *DB) PersonImageByID(id string) (*PersonImage, error) {
	return scanPersonImage(d.QueryRow(
		`SELECT id, name, image_url FROM persons WHERE id = ?`, id))
}

func (d *DB) PersonImageByName(name string) (*PersonImage, error) {
	return scanPersonImage(d.QueryRow(
		`SELECT id, name, image_url FROM persons WHERE name = ? COLLATE NOCASE LIMIT 1`, name))
}

// PersonNamesByIDs 把客户端传来的演员 ID 批量还原成名字。
func (d *DB) PersonNamesByIDs(ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		rows, err := d.Query(
			`SELECT id, name FROM persons WHERE id IN (`+placeholders(len(part))+`)`,
			toAnyArgs(part)...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = name
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListPersons 返回演员列表，供 /Persons 使用。
func (d *DB) ListPersons(search string, start, limit int) ([]PersonImage, int, error) {
	where := `name <> ''`
	args := []any{}
	if s := strings.TrimSpace(search); s != "" {
		where += ` AND name LIKE ? ESCAPE '\'`
		args = append(args, likeContains(s))
	}

	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM persons WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sqlText := `SELECT id, name, image_url FROM persons WHERE ` + where + ` ORDER BY name COLLATE NOCASE`
	if limit > 0 {
		sqlText += ` LIMIT ? OFFSET ?`
		args = append(args, limit, max0(start))
	} else if start > 0 {
		sqlText += ` LIMIT -1 OFFSET ?`
		args = append(args, start)
	}

	rows, err := d.Query(sqlText, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]PersonImage, 0, 32)
	for rows.Next() {
		var p PersonImage
		if err := rows.Scan(&p.ID, &p.Name, &p.ImageURL); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// BackfillPersonsFromItems 首次升级时把全部 items.people 展开进 persons 表。
// 之后由扫描/刮削增量维护，不再重复全量跑。
func (d *DB) BackfillPersonsFromItems() (int, error) {
	if v, err := d.GetSetting(personsBackfillSetting); err != nil {
		return 0, err
	} else if v == "done" {
		return 0, nil
	}

	n, err := d.indexPersons("", nil)
	if err != nil {
		return n, err
	}
	if err := d.SetSetting(personsBackfillSetting, "done"); err != nil {
		return n, err
	}
	return n, nil
}

// IndexPersonsFromLibrary 把某个库里所有条目的演员表同步进 persons。
// 本地 NFO 的演员只存在于 items.people，扫描完成后必须展开一次才能按 ID 筛选。
func (d *DB) IndexPersonsFromLibrary(libraryID string) (int, error) {
	if libraryID == "" {
		return 0, nil
	}
	return d.indexPersons("items.library_id = ?", []any{libraryID})
}

// IndexPersonsFromItems 增量刷新指定条目的演员。
func (d *DB) IndexPersonsFromItems(itemIDs []string) (int, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}
	seen := make(map[string]PersonImage, 256)
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		part := itemIDs[start:end]
		// 单集 NFO 里通常没有演员，演员挂在剧级。改动一集时把所属剧集
		// 一起展开，新增剧集的演员才不会漏索引。
		marks := placeholders(len(part))
		where := "items.id IN (" + marks + ") OR items.id IN (" +
			"SELECT series_id FROM items WHERE id IN (" + marks + ") AND series_id <> '')"
		args := append(toAnyArgs(part), toAnyArgs(part)...)
		if err := d.collectPersons(where, args, seen); err != nil {
			return 0, err
		}
	}
	return d.writePersons(seen)
}

func (d *DB) indexPersons(where string, args []any) (int, error) {
	seen := make(map[string]PersonImage, 256)
	if err := d.collectPersons(where, args, seen); err != nil {
		return 0, err
	}
	return d.writePersons(seen)
}

func (d *DB) collectPersons(where string, args []any, seen map[string]PersonImage) error {
	sqlText := `
SELECT DISTINCT TRIM(json_extract(p.value, '$.Name'))
FROM items, json_each(CASE WHEN json_valid(items.people) THEN items.people ELSE '[]' END) p
WHERE json_type(p.value) = 'object'
  AND TRIM(COALESCE(json_extract(p.value, '$.Name'), '')) <> ''`
	if where != "" {
		sqlText += " AND " + where
	}

	rows, err := d.Query(sqlText, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		id := PersonID(name)
		if _, ok := seen[id]; !ok {
			seen[id] = PersonImage{ID: id, Name: name}
		}
	}
	return rows.Err()
}

func (d *DB) writePersons(seen map[string]PersonImage) (int, error) {
	if len(seen) == 0 {
		return 0, nil
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	batch := make([]PersonImage, 0, 500)
	now := time.Now().Unix()
	for _, p := range seen {
		batch = append(batch, p)
		if len(batch) >= 500 {
			if err := flushPersons(tx, batch, now); err != nil {
				return 0, err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := flushPersons(tx, batch, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(seen), nil
}

func flushPersons(tx *sql.Tx, people []PersonImage, now int64) error {
	for _, p := range people {
		if err := upsertPerson(tx, p, now); err != nil {
			return err
		}
	}
	return nil
}

func likeContains(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

func scanPersonImage(row interface {
	Scan(dest ...any) error
}) (*PersonImage, error) {
	var p PersonImage
	if err := row.Scan(&p.ID, &p.Name, &p.ImageURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}
