-- 播放卡顿事件。除了触发 Telegram 告警，也留给后续分析：
-- 哪部片、哪个片源、什么设备最容易卡。
CREATE TABLE IF NOT EXISTS playback_stalls (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL DEFAULT '',
    user_name      TEXT NOT NULL DEFAULT '',
    device_id      TEXT NOT NULL DEFAULT '',
    device_name    TEXT NOT NULL DEFAULT '',
    client         TEXT NOT NULL DEFAULT '',
    session_id     TEXT NOT NULL DEFAULT '',
    item_id        TEXT NOT NULL DEFAULT '',
    item_name      TEXT NOT NULL DEFAULT '',
    item_type      TEXT NOT NULL DEFAULT '',
    source_id      TEXT NOT NULL DEFAULT '',
    position_ticks INTEGER NOT NULL DEFAULT 0,
    stalled_sec    INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    recovered_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_playback_stalls_created ON playback_stalls(created_at);
CREATE INDEX IF NOT EXISTS idx_playback_stalls_item ON playback_stalls(item_id);
CREATE INDEX IF NOT EXISTS idx_playback_stalls_session ON playback_stalls(session_id);
