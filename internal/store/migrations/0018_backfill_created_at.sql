-- created_at 之前是全库扫描时的 time.Now()，整库条目挤在同几秒，
-- 首页「最近添加 / DateLastContentAdded」排序退化成扫描顺序。
-- media_sources.strm_mtime 存的是 STRM 文件真实修改时间（PanSave 整理时间），
-- 用它回填一次即可让排序恢复成真实入库顺序。迁移只跑一次，幂等。

UPDATE items SET created_at = (
  SELECT MAX(ms.strm_mtime) FROM media_sources ms
  WHERE ms.item_id = items.id AND ms.strm_mtime > 0
)
WHERE type IN ('Episode', 'Movie')
  AND EXISTS (
    SELECT 1 FROM media_sources ms
    WHERE ms.item_id = items.id AND ms.strm_mtime > 0
  );

UPDATE items SET created_at = COALESCE((
  SELECT MAX(e.created_at) FROM items e
  WHERE e.type = 'Episode' AND e.series_id = items.id
), created_at)
WHERE type = 'Series';

-- 必须显式指定索引：否则优化器会选 idx_items_type_created 全扫单集表，
-- 每季一次，实测 682GB 读放大。parent_id 索引只扫本季的单集。
UPDATE items SET created_at = COALESCE((
  SELECT MAX(e.created_at) FROM items e INDEXED BY idx_items_parent_sort
  WHERE e.parent_id = items.id
), created_at)
WHERE type = 'Season';
