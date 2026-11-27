-- 剧集状态（TMDB 的 status 原文），用于返回 Jellyfin 的 Status 字段。
-- 老数据为空，重刮或补状态后才有值；空值不输出，客户端行为不变。
ALTER TABLE items ADD COLUMN series_status TEXT NOT NULL DEFAULT '';
