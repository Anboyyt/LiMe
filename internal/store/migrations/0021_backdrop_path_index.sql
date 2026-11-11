-- 背景图回填按 backdrop_path 批量替换同组条目（剧集 + 所有季/集）。
-- 没有索引时会每次全表扫描 26 万行，必须建。
CREATE INDEX IF NOT EXISTS idx_items_backdrop_path ON items(backdrop_path);
