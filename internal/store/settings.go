package store

import (
	"database/sql"
	"errors"
)

// GetSetting 读取运行时配置；不存在时返回空字符串。
func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting 写入运行时配置。
func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ServerID 返回持久化的服务器 ID，首次调用时生成。
func (d *DB) ServerID() (string, error) {
	v, err := d.GetSetting("server_id")
	if err != nil {
		return "", err
	}
	if v != "" {
		return v, nil
	}
	id := MustRandomHex(16)
	if err := d.SetSetting("server_id", id); err != nil {
		return "", err
	}
	return id, nil
}
