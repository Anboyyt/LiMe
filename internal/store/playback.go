package store

import (
	"strings"
	"time"
)

type PlaybackSession struct {
	ID            string
	UserID        string
	UserName      string
	DeviceID      string
	DeviceName    string
	Client        string
	ItemID        string
	PositionTicks int64
	IsPaused      bool
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// UpsertPlaybackSession 记录/更新一次播放会话。
func (d *DB) UpsertPlaybackSession(s *PlaybackSession) error {
	now := time.Now().Unix()
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Unix(now, 0)
	}
	_, err := d.Exec(`
INSERT INTO playback_sessions (id, user_id, device_id, item_id, position_ticks, is_paused, started_at, updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  user_id        = excluded.user_id,
  device_id      = excluded.device_id,
  item_id        = excluded.item_id,
  position_ticks = excluded.position_ticks,
  is_paused      = excluded.is_paused,
  updated_at     = excluded.updated_at`,
		s.ID, s.UserID, s.DeviceID, s.ItemID, s.PositionTicks, boolInt(s.IsPaused),
		s.StartedAt.Unix(), now,
	)
	return err
}

func (d *DB) DeletePlaybackSession(id string) error {
	_, err := d.Exec(`DELETE FROM playback_sessions WHERE id = ?`, id)
	return err
}

func (d *DB) ListPlaybackSessions() ([]*PlaybackSession, error) {
	rows, err := d.Query(`
SELECT s.id, s.user_id, COALESCE(u.name, ''), s.device_id,
       COALESCE(d.name, ''), COALESCE(d.client, ''),
       s.item_id, s.position_ticks, s.is_paused, s.started_at, s.updated_at
FROM playback_sessions s
LEFT JOIN users u ON u.id = s.user_id
LEFT JOIN devices d ON d.user_id = s.user_id AND d.device_id = s.device_id
ORDER BY s.updated_at DESC
LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*PlaybackSession, 0, 8)
	for rows.Next() {
		var (
			s     PlaybackSession
			pause int
			start int64
			upd   int64
		)
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.UserName, &s.DeviceID, &s.DeviceName, &s.Client,
			&s.ItemID, &s.PositionTicks,
			&pause, &start, &upd,
		); err != nil {
			return nil, err
		}
		s.IsPaused = pause != 0
		s.StartedAt = time.Unix(start, 0)
		s.UpdatedAt = time.Unix(upd, 0)
		out = append(out, &s)
	}
	return out, rows.Err()
}

// UpsertPlaystate 写入播放进度。played 一旦为真就不再回退。
func (d *DB) UpsertPlaystate(userID, itemID string, positionTicks int64, played bool) error {
	_, err := d.Exec(`
INSERT INTO playstates (user_id, item_id, position_ticks, played, is_favorite, updated_at)
VALUES (?,?,?,?,0,?)
ON CONFLICT(user_id, item_id) DO UPDATE SET
  position_ticks = excluded.position_ticks,
  played         = CASE WHEN excluded.played = 1 THEN 1 ELSE playstates.played END,
  updated_at     = excluded.updated_at`,
		userID, itemID, positionTicks, boolInt(played), time.Now().Unix(),
	)
	return err
}

// Playstate 是某用户在某条目上的观看状态。
type Playstate struct {
	ItemID        string
	PositionTicks int64
	Played        bool
	IsFavorite    bool
	UpdatedAt     time.Time
}

// WatchProgress 是剧集/季的观看聚合，用于客户端的「未看集数」角标。
type WatchProgress struct {
	Unplayed int
	Total    int
}

// Playstates 批量取播放状态；没有记录的条目不进 map。
func (d *DB) Playstates(userID string, itemIDs []string) (map[string]Playstate, error) {
	out := make(map[string]Playstate, len(itemIDs))
	if userID == "" || len(itemIDs) == 0 {
		return out, nil
	}

	// SQLite 变量数有上限，分批查。
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		ids := itemIDs[start:end]

		q := `SELECT item_id, position_ticks, played, is_favorite, updated_at
FROM playstates WHERE user_id = ? AND item_id IN (` + placeholders(len(ids)) + `)`
		args := make([]any, 0, len(ids)+1)
		args = append(args, userID)
		for _, id := range ids {
			args = append(args, id)
		}

		rows, err := d.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				ps  Playstate
				pl  int
				fav int
				upd int64
			)
			if err := rows.Scan(&ps.ItemID, &ps.PositionTicks, &pl, &fav, &upd); err != nil {
				rows.Close()
				return nil, err
			}
			ps.Played = pl != 0
			ps.IsFavorite = fav != 0
			ps.UpdatedAt = time.Unix(upd, 0)
			out[ps.ItemID] = ps
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ResumeItemIDs 返回「继续观看」条目：有进度且未看完，按最近播放倒序。
// libraryIDs 为空表示不限库；maxRating > 0 时过滤超过家长分级的条目。
// 两个过滤条件都进 SQL，保证 TotalRecordCount 和分页一致。
func (d *DB) ResumeItemIDs(userID string, types, libraryIDs []string, maxRating, limit, start int) ([]string, int, error) {
	where := []string{"p.user_id = ?", "p.played = 0", "p.position_ticks > 0"}
	args := []any{userID}
	if len(types) > 0 {
		where = append(where, "i.type IN ("+placeholders(len(types))+")")
		for _, t := range types {
			args = append(args, t)
		}
	}
	if len(libraryIDs) > 0 {
		where = append(where, "i.library_id IN ("+placeholders(len(libraryIDs))+")")
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	if maxRating > 0 {
		where = append(where, "(i.rating_score = 0 OR i.rating_score <= ?)")
		args = append(args, maxRating)
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := d.QueryRow(
		`SELECT COUNT(*) FROM playstates p JOIN items i ON i.id = p.item_id WHERE `+cond,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 20
	}
	q := `SELECT p.item_id FROM playstates p JOIN items i ON i.id = p.item_id
WHERE ` + cond + ` ORDER BY p.updated_at DESC LIMIT ? OFFSET ?`

	rows, err := d.Query(q, append(args, limit, max0(start))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	return ids, total, rows.Err()
}

// SetPlayed 标记已看/未看。标记已看时把进度清零，符合 Jellyfin 的语义。
func (d *DB) SetPlayed(userID, itemID string, played bool) error {
	_, err := d.Exec(`
INSERT INTO playstates (user_id, item_id, position_ticks, played, is_favorite, updated_at)
VALUES (?,?,0,?,0,?)
ON CONFLICT(user_id, item_id) DO UPDATE SET
  played = excluded.played,
  position_ticks = CASE WHEN excluded.played = 1 THEN 0 ELSE playstates.position_ticks END,
  updated_at = excluded.updated_at`,
		userID, itemID, boolInt(played), time.Now().Unix())
	return err
}

// SetFavorite 切换收藏，不影响进度和已看状态。
func (d *DB) SetFavorite(userID, itemID string, favorite bool) error {
	_, err := d.Exec(`
INSERT INTO playstates (user_id, item_id, position_ticks, played, is_favorite, updated_at)
VALUES (?,?,0,0,?,?)
ON CONFLICT(user_id, item_id) DO UPDATE SET
  is_favorite = excluded.is_favorite,
  updated_at = excluded.updated_at`,
		userID, itemID, boolInt(favorite), time.Now().Unix())
	return err
}

// WatchProgress 统计剧集/季下有多少集没看。
// column 只接受 "series_id" 或 "season_id"（不接受外部输入，调用方写死）。
func (d *DB) WatchProgress(userID, column string, ids []string) (map[string]WatchProgress, error) {
	out := make(map[string]WatchProgress, len(ids))
	if userID == "" || len(ids) == 0 {
		return out, nil
	}

	// 400 一条时每块只按 400 个 series_id 走索引，7159 部剧要 3.2s；
	// 4000 一条降到 0.6s。modernc 的变量上限是 32766，4000 很安全。
	const chunk = 4000
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]

		q := `SELECT i.` + column + `,
       COUNT(*),
       SUM(CASE WHEN COALESCE(p.played, 0) = 0 THEN 1 ELSE 0 END)
FROM items i
LEFT JOIN playstates p ON p.user_id = ? AND p.item_id = i.id
WHERE i.type = 'Episode' AND i.` + column + ` IN (` + placeholders(len(part)) + `)
GROUP BY i.` + column

		args := make([]any, 0, len(part)+1)
		args = append(args, userID)
		for _, id := range part {
			args = append(args, id)
		}

		rows, err := d.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				id    string
				total int
				unwat int
			)
			if err := rows.Scan(&id, &total, &unwat); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = WatchProgress{Unplayed: unwat, Total: total}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
