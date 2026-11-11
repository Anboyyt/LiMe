package store

import (
	"database/sql"
	"errors"
	"time"
)

func (d *DB) CreateToken(userID, deviceID string) (string, error) {
	token, err := randomHex(16) // 32 hex 字符
	if err != nil {
		return "", err
	}
	_, err = d.Exec(
		`INSERT INTO access_tokens (token, user_id, device_id, created_at) VALUES (?, ?, ?, ?)`,
		token, userID, deviceID, time.Now().Unix(),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (d *DB) UserByToken(token string) (*User, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	var userID string
	err := d.QueryRow(
		`SELECT user_id FROM access_tokens
		 WHERE token = ? AND (expires_at IS NULL OR expires_at > ?)`,
		token, time.Now().Unix(),
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	user, err := d.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	if user.IsDisabled {
		return nil, ErrNotFound
	}
	return user, nil
}

// APIToken 是给第三方客户端/脚本用的长期 key。
type APIToken struct {
	Token     string
	UserID    string
	UserName  string
	Name      string
	CreatedAt time.Time
}

func (d *DB) CreateAPIToken(userID, app string) (string, error) {
	token, err := randomHex(16)
	if err != nil {
		return "", err
	}
	_, err = d.Exec(
		`INSERT INTO access_tokens (token, user_id, device_id, name, created_at)
		 VALUES (?, ?, 'api-key', ?, ?)`,
		token, userID, app, time.Now().Unix(),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ListAPITokens 只列 API Key，不列登录 token。
func (d *DB) ListAPITokens() ([]*APIToken, error) {
	rows, err := d.Query(`
SELECT t.token, t.user_id, COALESCE(u.name, ''), t.name, t.created_at
FROM access_tokens t
LEFT JOIN users u ON u.id = t.user_id
WHERE t.name <> ''
ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*APIToken, 0, 4)
	for rows.Next() {
		var (
			t       APIToken
			created int64
		)
		if err := rows.Scan(&t.Token, &t.UserID, &t.UserName, &t.Name, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// DeleteTokensForUserExcept 改密码后让其他设备重新登录，保留当前会话。
func (d *DB) DeleteTokensForUserExcept(userID, keep string) error {
	_, err := d.Exec(`DELETE FROM access_tokens WHERE user_id = ? AND token <> ?`, userID, keep)
	return err
}

func (d *DB) DeleteToken(token string) error {
	_, err := d.Exec(`DELETE FROM access_tokens WHERE token = ?`, token)
	return err
}

func (d *DB) DeleteTokensForUser(userID string) error {
	_, err := d.Exec(`DELETE FROM access_tokens WHERE user_id = ?`, userID)
	return err
}
