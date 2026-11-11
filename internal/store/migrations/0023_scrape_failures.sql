CREATE TABLE IF NOT EXISTS scrape_failures (
  item_id    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_scrape_failures_kind ON scrape_failures(kind);
CREATE INDEX IF NOT EXISTS idx_scrape_failures_updated ON scrape_failures(updated_at);
