-- 片头/片尾片段。按「集」存，每集一条或多条，不存整季偏移：
-- 同一部剧不同集的片头位置经常随冷开场/Recap 长度漂移。
CREATE TABLE IF NOT EXISTS media_segments (
    id          TEXT PRIMARY KEY,
    item_id     TEXT NOT NULL,
    type        TEXT NOT NULL,              -- Intro / Outro / Recap / Preview / Commercial
    start_ticks INTEGER NOT NULL,
    end_ticks   INTEGER NOT NULL,
    source      TEXT NOT NULL DEFAULT 'chapter', -- chapter / fingerprint / manual
    confidence  REAL NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_segments_item ON media_segments(item_id);
CREATE INDEX IF NOT EXISTS idx_media_segments_item_type ON media_segments(item_id, type);

-- 每个条目的片段分析状态。analysis 只跑一次，避免预热每晚重复扫描。
CREATE TABLE IF NOT EXISTS segment_analysis (
    item_id    TEXT PRIMARY KEY,
    status     TEXT NOT NULL,               -- done / failed
    method     TEXT NOT NULL DEFAULT '',    -- chapter / fingerprint / manual
    note       TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_segment_analysis_status ON segment_analysis(status);
