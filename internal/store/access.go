package store

// LibraryAccess 返回用户显式授权的媒体库。
// 空列表表示没配置过，按默认放开全部库处理。
func (d *DB) LibraryAccess(userID string) ([]string, error) {
	rows, err := d.Query(`SELECT library_id FROM user_library_access WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetLibraryAccess 覆盖用户的库权限；传空表示恢复默认（全部可见）。
func (d *DB) SetLibraryAccess(userID string, libraryIDs []string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_library_access WHERE user_id = ?`, userID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, id := range libraryIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO user_library_access (user_id, library_id) VALUES (?, ?)`,
			userID, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
