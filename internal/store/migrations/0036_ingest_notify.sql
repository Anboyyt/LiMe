-- 入库通知队列：新片源写入时 ingested_at=now/notified_at=0；
-- 已存在片源更新时保留原值，避免重复通知。
-- 存量行保持 ingested_at=0，worker 会跳过，不会升级后刷屏。
ALTER TABLE media_sources ADD COLUMN ingested_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_sources ADD COLUMN notified_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_media_sources_ingest_pending
    ON media_sources(notified_at, ingested_at)
    WHERE notified_at = 0 AND ingested_at > 0;
