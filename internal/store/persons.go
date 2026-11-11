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

// PersonID 用演员名生成稳定的 32 位十六进制 ID，和 Jellyfin 的 ID 形态一致。
func PersonID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	sum := sha256.Sum256([]byte("person:" + name))
	return hex.EncodeToString(sum[:16])
}

// UpsertPersonImages 批量记录演员头像地址；同一演员只保留一条。
func (d *DB) UpsertPersonImages(people []PersonImage) error {
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
		if p.ID == "" || p.Name == "" || p.ImageURL == "" {
			continue
		}
		if _, err := tx.Exec(`
INSERT INTO person_images (id, name, image_url, updated_at) VALUES (?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  image_url = excluded.image_url,
  updated_at = excluded.updated_at`,
			p.ID, p.Name, p.ImageURL, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) PersonImageByID(id string) (*PersonImage, error) {
	return scanPersonImage(d.QueryRow(
		`SELECT id, name, image_url FROM person_images WHERE id = ?`, id))
}

func (d *DB) PersonImageByName(name string) (*PersonImage, error) {
	return scanPersonImage(d.QueryRow(
		`SELECT id, name, image_url FROM person_images WHERE name = ? LIMIT 1`, name))
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
