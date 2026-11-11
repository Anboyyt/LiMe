-- 拼音排序与搜索。
-- sort_name 保持不动：它同时是 itemID 的种子，直接改会让已入库条目换 ID。
-- sort_key 独立承担排序，空值时回退 sort_name，索引建在同一个表达式上。
ALTER TABLE items ADD COLUMN sort_key TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN pinyin TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN pinyin_abbr TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_items_lib_sortkey
    ON items(library_id, COALESCE(NULLIF(sort_key, ''), sort_name));
CREATE INDEX IF NOT EXISTS idx_items_lib_type_sortkey
    ON items(library_id, type, COALESCE(NULLIF(sort_key, ''), sort_name));
CREATE INDEX IF NOT EXISTS idx_items_parent_sortkey
    ON items(parent_id, COALESCE(NULLIF(sort_key, ''), sort_name));
CREATE INDEX IF NOT EXISTS idx_items_pinyin ON items(pinyin);
CREATE INDEX IF NOT EXISTS idx_items_pinyin_abbr ON items(pinyin_abbr);
