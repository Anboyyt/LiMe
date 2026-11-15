-- 演员筛选关联表。items.people 是 JSON，直接 json_each 过滤要全表扫；
-- 这里把「条目 → 演员名」展开成索引表，/Items?PersonIds= 和演员过滤走索引。
-- 触发器在 Go 里单独创建（迁移文件按分号切分，BEGIN...END 会被切坏）。
CREATE TABLE IF NOT EXISTS item_persons (
    item_id    TEXT NOT NULL,
    name       TEXT NOT NULL,
    name_lower TEXT NOT NULL,
    PRIMARY KEY (item_id, name_lower)
);
CREATE INDEX IF NOT EXISTS idx_item_persons_name ON item_persons(name_lower, item_id);
