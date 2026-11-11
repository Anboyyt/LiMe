package store

import (
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID                string
	Name              string
	PasswordHash      string
	IsAdmin           bool
	IsDisabled        bool
	MaxParentalRating int // 0 = 不限制
	CreatedAt         time.Time
	LastLoginAt       *time.Time
}

// ---------- 口令哈希（argon2id） ----------

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := randRead(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}

	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- 查询 ----------

func (d *DB) CreateUser(name, passwordHash string, isAdmin bool) (*User, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	_, err = d.Exec(
		`INSERT INTO users (id, name, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, passwordHash, boolToInt(isAdmin), now,
	)
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Name: name, PasswordHash: passwordHash, IsAdmin: isAdmin, CreatedAt: time.Unix(now, 0)}, nil
}

func (d *DB) GetUserByName(name string) (*User, error) {
	return d.scanUser(d.QueryRow(
		`SELECT id, name, password_hash, is_admin, is_disabled, max_parental_rating, created_at, last_login_at FROM users WHERE name = ?`, name,
	))
}

func (d *DB) GetUserByID(id string) (*User, error) {
	return d.scanUser(d.QueryRow(
		`SELECT id, name, password_hash, is_admin, is_disabled, max_parental_rating, created_at, last_login_at FROM users WHERE id = ?`, id,
	))
}

func (d *DB) ListUsers() ([]*User, error) {
	rows, err := d.Query(
		`SELECT id, name, password_hash, is_admin, is_disabled, max_parental_rating, created_at, last_login_at FROM users ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]*User, 0, 4)
	for rows.Next() {
		u, err := d.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) scanUser(row interface {
	Scan(dest ...any) error
}) (*User, error) {
	var (
		u           User
		createdAt   int64
		lastLoginAt sql.NullInt64
		isAdmin     int
		isDisabled  int
		maxRating   int
	)
	if err := row.Scan(&u.ID, &u.Name, &u.PasswordHash, &isAdmin, &isDisabled, &maxRating, &createdAt, &lastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsDisabled = isDisabled != 0
	u.MaxParentalRating = maxRating
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastLoginAt.Valid {
		t := time.Unix(lastLoginAt.Int64, 0)
		u.LastLoginAt = &t
	}
	return &u, nil
}

func (d *DB) TouchLogin(userID string) {
	_, _ = d.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), userID)
}

// EnsureAdmin 在用户表为空时创建初始管理员。
func (d *DB) EnsureAdmin(name, password string) error {
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := d.CreateUser(name, hash, true); err != nil {
		return err
	}
	slog.Info("created initial admin user", "name", name)
	return nil
}

func (d *DB) UpdateUserName(userID, name string) error {
	_, err := d.Exec(`UPDATE users SET name = ? WHERE id = ?`, name, userID)
	return err
}

func (d *DB) UpdatePassword(userID, passwordHash string) error {
	_, err := d.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return err
}

func (d *DB) SetUserAdmin(userID string, admin bool) error {
	_, err := d.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, boolToInt(admin), userID)
	return err
}

// SetUserMaxRating 设置用户最高允许分级，0 表示不限制。
func (d *DB) SetUserMaxRating(userID string, rating int) error {
	_, err := d.Exec(`UPDATE users SET max_parental_rating = ? WHERE id = ?`, rating, userID)
	return err
}

// SetUserDisabled 禁用用户时顺手吊销其所有 token。
func (d *DB) SetUserDisabled(userID string, disabled bool) error {
	if _, err := d.Exec(`UPDATE users SET is_disabled = ? WHERE id = ?`, boolToInt(disabled), userID); err != nil {
		return err
	}
	if disabled {
		return d.DeleteTokensForUser(userID)
	}
	return nil
}

func (d *DB) DeleteUser(userID string) error {
	_, err := d.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

func (d *DB) CountAdmins() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n, err
}

// ---------- 设备 ----------

func (d *DB) UpsertDevice(userID, deviceID, client, name string) error {
	if deviceID == "" {
		deviceID = "unknown"
	}
	now := time.Now().Unix()
	_, err := d.Exec(
		`INSERT INTO devices (id, user_id, device_id, client, name, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, device_id) DO UPDATE SET
		   client = excluded.client,
		   name = excluded.name,
		   last_seen_at = excluded.last_seen_at`,
		mustID(), userID, deviceID, client, name, now,
	)
	return err
}

// ---------- 工具 ----------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func AtoiOr0(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
