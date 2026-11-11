-- 大库（26 万 item / 29 万片源）查询优化。
-- 客户端按库列表常用 SortBy=DateLastContentAdded,DateCreated,SortName，
-- 实际生效的是 created_at DESC + sort_name DESC；没有覆盖索引时会走
-- 临时 B-Tree 并回表整库，HDD 上单次请求接近 1 秒。
CREATE INDEX IF NOT EXISTS idx_items_lib_created_sort
    ON items(library_id, created_at DESC, sort_name DESC);

-- /Items/Latest：按库分组取每部剧最新一集的入库时间。
CREATE INDEX IF NOT EXISTS idx_items_lib_type_series_created
    ON items(library_id, type, series_id, created_at DESC);

-- 原生界面的「最新一集」排序：相关子查询取 MAX(premiere_date) / MAX(created_at)。
CREATE INDEX IF NOT EXISTS idx_items_series_premiere
    ON items(series_id, premiere_date);
CREATE INDEX IF NOT EXISTS idx_items_series_created
    ON items(series_id, created_at);

-- idx_items_type 只含 type 单列，会让 ORDER BY created_at 的查询选错计划；
-- idx_items_type_created(type, created_at) 已覆盖它的全部用途。
DROP INDEX IF EXISTS idx_items_type;
