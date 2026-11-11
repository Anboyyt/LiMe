-- 用户最高允许分级（0 = 不限制）
ALTER TABLE users ADD COLUMN max_parental_rating INTEGER NOT NULL DEFAULT 0;

-- 条目的分级分值（0 = 未分级/未知），用于列表过滤，避免每次请求解析字符串
ALTER TABLE items ADD COLUMN rating_score INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_items_rating_score ON items(rating_score);
