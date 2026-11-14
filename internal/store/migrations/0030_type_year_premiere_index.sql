-- 客户端/网页的「年份」列表几乎都带 IncludeItemTypes，
-- 只有 (production_year, premiere_date) 或 (library_id, production_year, premiere_date)
-- 时 SQLite 会先按 type 过滤再临时排序，大库上单次 500ms 级。
-- 把 type 放进索引前缀，让 ORDER BY production_year DESC, premiere_date DESC 直接走索引。
CREATE INDEX IF NOT EXISTS idx_items_type_year_premiere
    ON items(type, production_year, premiere_date);

CREATE INDEX IF NOT EXISTS idx_items_library_type_year_premiere
    ON items(library_id, type, production_year, premiere_date);
