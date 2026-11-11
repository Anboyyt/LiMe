package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

type Library struct {
	ID        string
	Name      string
	Path      string   // 第一个路径，兼容旧字段
	Paths     []string // 全部路径（一个库可以有多个文件夹）
	Kind      string   // movies | tvshows | mixed | auto
	CreatedAt time.Time
}

var ErrLibraryPathExists = errors.New("library path already exists")

func normalizePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// checkPathConflicts 检查路径是否已被其他库占用。
func (d *DB) checkPathConflicts(paths []string, excludeID string) error {
	if len(paths) == 0 {
		return nil
	}
	q := `SELECT COUNT(*) FROM library_paths WHERE path IN (` + placeholders(len(paths)) + `)`
	args := make([]any, 0, len(paths)+1)
	for _, p := range paths {
		args = append(args, p)
	}
	if excludeID != "" {
		q += ` AND library_id <> ?`
		args = append(args, excludeID)
	}
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrLibraryPathExists
	}
	return nil
}

// pathsForLibraries 一次取回多个库的全部路径，避免逐个查询。
func (d *DB) pathsForLibraries(ids []string) (map[string][]string, error) {
	out := make(map[string][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := d.Query(
		`SELECT library_id, path FROM library_paths WHERE library_id IN (`+
			placeholders(len(ids))+`) ORDER BY path`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		out[id] = append(out[id], path)
	}
	return out, rows.Err()
}

func saveLibraryPaths(tx *sql.Tx, id string, paths []string) error {
	if _, err := tx.Exec(`DELETE FROM library_paths WHERE library_id = ?`, id); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO library_paths (library_id, path) VALUES (?, ?)`, id, p); err != nil {
			return err
		}
	}
	return nil
}

// CreateLibrary 新建库；任一文件夹路径已被其他库占用时返回 ErrLibraryPathExists。
func (d *DB) CreateLibrary(name string, paths []string, kind string) (*Library, error) {
	paths = normalizePaths(paths)
	if len(paths) == 0 {
		return nil, errors.New("library path is required")
	}
	if err := d.checkPathConflicts(paths, ""); err != nil {
		return nil, err
	}

	id := mustID()
	now := time.Now()
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`INSERT INTO libraries (id, name, path, kind, created_at, sort_order)
		 VALUES (?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort_order), -1) + 1 FROM libraries))`,
		id, name, paths[0], kind, now.Unix()); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := saveLibraryPaths(tx, id, paths); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Library{ID: id, Name: name, Path: paths[0], Paths: paths, Kind: kind, CreatedAt: now}, nil
}

// UpdateLibrary 改名、改文件夹列表或改类型。
func (d *DB) UpdateLibrary(id, name string, paths []string, kind string) error {
	paths = normalizePaths(paths)
	if len(paths) == 0 {
		return errors.New("library path is required")
	}
	if err := d.checkPathConflicts(paths, id); err != nil {
		return err
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE libraries SET name = ?, path = ?, kind = ? WHERE id = ?`,
		name, paths[0], kind, id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}
	if err := saveLibraryPaths(tx, id, paths); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DeleteLibrary 删除库；items/media_sources/playstates 由外键级联清理。
func (d *DB) DeleteLibrary(id string) error {
	res, err := d.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertLibrary 以路径为唯一键，重复配置时更新名称与类型（LIME_LIBRARIES 种子用）。
func (d *DB) UpsertLibrary(name, path, kind string) (*Library, error) {
	path = filepath.Clean(path)

	var id string
	err := d.QueryRow(`SELECT library_id FROM library_paths WHERE path = ?`, path).Scan(&id)
	switch {
	case err == nil:
		if _, err = d.Exec(`UPDATE libraries SET name = ?, kind = ? WHERE id = ?`, name, kind, id); err != nil {
			return nil, err
		}
	case errors.Is(err, sql.ErrNoRows):
		id = mustID()
		tx, err := d.Begin()
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(
			`INSERT INTO libraries (id, name, path, kind, created_at, sort_order)
			 VALUES (?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort_order), -1) + 1 FROM libraries))`,
			id, name, path, kind, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = saveLibraryPaths(tx, id, []string{path}); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	return &Library{ID: id, Name: name, Path: path, Paths: []string{path}, Kind: kind}, nil
}

func (d *DB) ListLibraries() ([]*Library, error) {
	rows, err := d.Query(`SELECT id, name, path, kind, created_at FROM libraries ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	out := make([]*Library, 0, 4)
	ids := make([]string, 0, 4)
	for rows.Next() {
		var (
			lib       Library
			createdAt int64
		)
		if err := rows.Scan(&lib.ID, &lib.Name, &lib.Path, &lib.Kind, &createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		lib.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, &lib)
		ids = append(ids, lib.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	paths, err := d.pathsForLibraries(ids)
	if err != nil {
		return nil, err
	}
	for _, lib := range out {
		lib.Paths = paths[lib.ID]
		if len(lib.Paths) == 0 && lib.Path != "" {
			lib.Paths = []string{lib.Path}
		}
	}
	return out, nil
}

func (d *DB) GetLibrary(id string) (*Library, error) {
	var (
		lib       Library
		createdAt int64
	)
	err := d.QueryRow(
		`SELECT id, name, path, kind, created_at FROM libraries WHERE id = ?`, id,
	).Scan(&lib.ID, &lib.Name, &lib.Path, &lib.Kind, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	lib.CreatedAt = time.Unix(createdAt, 0)

	rows, err := d.Query(`SELECT path FROM library_paths WHERE library_id = ? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		lib.Paths = append(lib.Paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(lib.Paths) == 0 && lib.Path != "" {
		lib.Paths = []string{lib.Path}
	}
	return &lib, nil
}

func (d *DB) UpdateLibraryKind(id, kind string) error {
	_, err := d.Exec(`UPDATE libraries SET kind = ? WHERE id = ?`, kind, id)
	return err
}

// MoveLibrary 上移/下移一个库，重排所有库的 sort_order。
func (d *DB) MoveLibrary(id string, up bool) error {
	libs, err := d.ListLibraries()
	if err != nil {
		return err
	}
	idx := -1
	for i, l := range libs {
		if l.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	to := idx - 1
	if !up {
		to = idx + 1
	}
	if to < 0 || to >= len(libs) {
		return nil
	}
	libs[idx], libs[to] = libs[to], libs[idx]

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	for i, l := range libs {
		if _, err := tx.Exec(`UPDATE libraries SET sort_order = ? WHERE id = ?`, i, l.ID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ReorderLibraries 按给定 id 顺序重写 sort_order。
func (d *DB) ReorderLibraries(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE libraries SET sort_order = ? WHERE id = ?`, i, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
