-- 「年份」排序改为 年份 + 开播/上映日期，避免同年条目按索引扫描顺序返回。
-- 复合索引让 ORDER BY production_year DESC, premiere_date DESC 直接走索引，
-- 大库（约 29 万 item）不用临时 B-Tree 全量排序。
CREATE INDEX IF NOT EXISTS idx_items_year_premiere
    ON items(production_year, premiere_date);

CREATE INDEX IF NOT EXISTS idx_items_library_year_premiere
    ON items(library_id, production_year, premiere_date);
