-- 剧/季本身没有播放进度。历史版本接收了客户端把 Series 当播放项
-- 上报的数据，这些记录会混进续播和未看统计，清掉。
DELETE FROM playstates
WHERE item_id IN (SELECT id FROM items WHERE type IN ('Series', 'Season'));
