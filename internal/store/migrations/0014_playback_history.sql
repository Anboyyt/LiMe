-- 播放历史：供运维面板查看，保留一周。
-- user_name / item_name 冗余存储，用户或条目删除后历史仍可读。
CREATE TABLE IF NOT EXISTS playback_history (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        TEXT NOT NULL,
    user_name      TEXT NOT NULL DEFAULT '',
    device_id      TEXT NOT NULL DEFAULT '',
    device_name    TEXT NOT NULL DEFAULT '',
    item_id        TEXT NOT NULL,
    item_name      TEXT NOT NULL DEFAULT '',
    item_type      TEXT NOT NULL DEFAULT '',
    event          TEXT NOT NULL,          -- start / pause / unpause / stop
    position_ticks INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_playback_history_created ON playback_history(created_at);
