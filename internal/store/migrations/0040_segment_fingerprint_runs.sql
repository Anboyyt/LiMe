-- 记录每一季音频指纹分析的尝试，避免没有片头片尾的剧每晚重复扫。
-- 回填调度只处理「还没有尝试过（或超过重试间隔）」的季。
CREATE TABLE IF NOT EXISTS segment_fingerprint_runs (
  series_id TEXT NOT NULL,
  season INTEGER NOT NULL,
  status TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (series_id, season)
);

CREATE INDEX IF NOT EXISTS idx_segment_fingerprint_updated
  ON segment_fingerprint_runs(updated_at);
