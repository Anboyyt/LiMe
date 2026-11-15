-- 类型筛选关联表，作用同 item_persons：避免 genres JSON 全表展开。
-- 触发器在 Go 里创建。
CREATE TABLE IF NOT EXISTS item_genres (
    item_id TEXT NOT NULL,
    genre   TEXT NOT NULL,
    PRIMARY KEY (item_id, genre)
);
CREATE INDEX IF NOT EXISTS idx_item_genres_genre ON item_genres(genre, item_id);
