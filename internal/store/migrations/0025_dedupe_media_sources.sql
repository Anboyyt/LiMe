-- 同一 strm_path 只保留最新的一条片源。
-- 旧版本在 STRM 被重新生成（file_id 变化）时只做 upsert，不清理旧行，
-- 客户端因此会看到重复或已失效的版本。扫描器已改为写入前先删除，
-- 这里一次性清掉历史残留；组内所有行都属于同一个 item。
DELETE FROM media_sources
WHERE id IN (
  SELECT id FROM (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY strm_path ORDER BY updated_at DESC, id DESC) AS rn
    FROM media_sources
    WHERE strm_path <> ''
  )
  WHERE rn > 1
);
