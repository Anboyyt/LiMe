-- 面板「精准提取」：按剧名搜索后把指定剧的季排进音频指纹队列。
-- 队列落库，进程重启不会丢进度。
CREATE TABLE IF NOT EXISTS segment_targets (
  series_id  TEXT NOT NULL,
  season     INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT 'queued',
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (series_id, season)
);

CREATE INDEX IF NOT EXISTS idx_segment_targets_status
  ON segment_targets(status, created_at);
