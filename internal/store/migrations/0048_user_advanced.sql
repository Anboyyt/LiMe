-- 允许播放时段，JSON：{"Start":"18:00","End":"23:00","Days":[1,2,3,4,5,6,7]}
-- 空字符串表示不限制。Start > End 视为跨夜（如 22:00-02:00）。
ALTER TABLE users ADD COLUMN play_window TEXT NOT NULL DEFAULT '';

-- 登录流水。devices 只保留最后一次，这里留历史，用于排查异常登录。
CREATE TABLE IF NOT EXISTS login_log (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    user_name  TEXT NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    client     TEXT NOT NULL DEFAULT '',
    device     TEXT NOT NULL DEFAULT '',
    success    INTEGER NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_login_log_created ON login_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_login_log_user ON login_log(user_id, created_at DESC);
