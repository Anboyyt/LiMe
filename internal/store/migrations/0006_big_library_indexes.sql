-- 大库（20 万+ item）常用查询索引
CREATE INDEX IF NOT EXISTS idx_items_created ON items(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_type_created ON items(type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_parent_sort ON items(parent_id, sort_name);
CREATE INDEX IF NOT EXISTS idx_items_series_season_ep ON items(series_id, parent_index_number, index_number);
CREATE INDEX IF NOT EXISTS idx_items_library_type_sort ON items(library_id, type, sort_name);
CREATE INDEX IF NOT EXISTS idx_items_library_created ON items(library_id, created_at DESC);
