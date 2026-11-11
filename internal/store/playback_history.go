package store

import (
	"database/sql"
	"time"
)

// PlaybackHistoryRetention 是播放记录的保留期。
const PlaybackHistoryRetention = 7 * 24 * time.Hour

// PlaybackHistory 是一条播放事件（开始/暂停/继续/停止）。
type PlaybackHistory struct {
	ID            int64
	UserID        string
	UserName      string
	DeviceID      string
	DeviceName    string
	ItemID        string
	ItemName      string
	ItemType      string
	Event         string
	PositionTicks int64
	CreatedAt     time.Time
}

// AddPlaybackHistory 写入一条记录，并顺手清掉超过保留期的旧记录。
func (d *DB) AddPlaybackHistory(h *PlaybackHistory) error {
	now := time.Now()
	if h.CreatedAt.IsZero() {
		h.CreatedAt = now
	}

	if _, err := d.Exec(
		`DELETE FROM playback_history WHERE created_at < ?`,
		now.Add(-PlaybackHistoryRetention).Unix(),
	); err != nil {
		return err
	}

	_, err := d.Exec(`
INSERT INTO playback_history (
  user_id, user_name, device_id, device_name,
  item_id, item_name, item_type, event, position_ticks, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		h.UserID, h.UserName, h.DeviceID, h.DeviceName,
		h.ItemID, h.ItemName, h.ItemType, h.Event, h.PositionTicks,
		h.CreatedAt.Unix(),
	)
	return err
}

// PlaybackHistory 按时间倒序返回保留期内的记录。
func (d *DB) PlaybackHistory(limit int) ([]*PlaybackHistory, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	cutoff := time.Now().Add(-PlaybackHistoryRetention).Unix()

	rows, err := d.Query(`
SELECT id, user_id, user_name, device_id, device_name,
       item_id, item_name, item_type, event, position_ticks, created_at
FROM playback_history
WHERE created_at >= ?
ORDER BY created_at DESC, id DESC
LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*PlaybackHistory, 0, 32)
	for rows.Next() {
		var (
			h       PlaybackHistory
			created int64
		)
		if err := rows.Scan(
			&h.ID, &h.UserID, &h.UserName, &h.DeviceID, &h.DeviceName,
			&h.ItemID, &h.ItemName, &h.ItemType, &h.Event,
			&h.PositionTicks, &created,
		); err != nil {
			return nil, err
		}
		h.CreatedAt = time.Unix(created, 0)
		out = append(out, &h)
	}
	return out, rows.Err()
}

// PlaybackSessionPaused 返回某个播放会话上一次的暂停状态。
func (d *DB) PlaybackSessionPaused(id string) (paused bool, found bool, err error) {
	var v int
	err = d.QueryRow(`SELECT is_paused FROM playback_sessions WHERE id = ?`, id).Scan(&v)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return v != 0, true, nil
}
