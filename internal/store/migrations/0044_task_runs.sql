-- 扫描 / 刮削 / 备份的最近执行记录，供面板回溯。
CREATE TABLE IF NOT EXISTS task_runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  kind        TEXT NOT NULL,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL,
  status      TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_task_runs_started
  ON task_runs(started_at DESC);
