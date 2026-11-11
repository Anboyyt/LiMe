-- 实时监控删除同步：按 strm_path 精确删除、按目录前缀清理。
CREATE INDEX IF NOT EXISTS idx_media_sources_strm ON media_sources(strm_path);
