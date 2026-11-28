package store

import (
	"time"
)

// LoginLog 是一条登录尝试记录。失败也要记，排查异常登录时才有上下文。
type LoginLog struct {
	ID        string
	UserID    string
	UserName  string
	IP        string
	Client    string
	Device    string
	Success   bool
	Reason    string
	CreatedAt time.Time
}

// LoginLogRetention 是登录流水的保留时间。
const LoginLogRetention = 30 * 24 * time.Hour

func (d *DB) AddLoginLog(e *LoginLog) error {
	_, err := d.Exec(
		`INSERT INTO login_log (id, user_id, user_name, ip, client, device, success, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mustID(), e.UserID, e.UserName, e.IP, e.Client, e.Device,
		boolToInt(e.Success), e.Reason, time.Now().Unix(),
	)
	return err
}

// LoginLogs 返回登录记录；userID 为空表示全部用户。
func (d *DB) LoginLogs(userID string, limit int) ([]*LoginLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, user_name, ip, client, device, success, reason, created_at
	        FROM login_log`
	args := make([]any, 0, 2)
	if userID != "" {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	// 同一秒内可能连续写多条，再用 rowid 保证「后写的在前」。
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*LoginLog, 0, limit)
	for rows.Next() {
		var (
			v       LoginLog
			success int
			at      int64
		)
		if err := rows.Scan(&v.ID, &v.UserID, &v.UserName, &v.IP, &v.Client, &v.Device,
			&success, &v.Reason, &at); err != nil {
			return nil, err
		}
		v.Success = success != 0
		v.CreatedAt = time.Unix(at, 0)
		out = append(out, &v)
	}
	return out, rows.Err()
}

func (d *DB) PruneLoginLogs(before time.Time) (int64, error) {
	res, err := d.Exec(`DELETE FROM login_log WHERE created_at < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
