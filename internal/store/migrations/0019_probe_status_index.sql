-- 预热探测按 probe_status 过滤（29 万条），没有索引时容器冷启动后
-- 第一次统计/取批次要扫整张表，实测设置页首次加载 23 秒。
CREATE INDEX IF NOT EXISTS idx_media_sources_probe ON media_sources(probe_status);
