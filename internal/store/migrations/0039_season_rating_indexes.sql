-- 季列表、继续观看、NextUp 都按 season_id 过滤，之前只有 series_id 索引，
-- 只能先扫出整部剧的单集再逐行比较。29 万条目下这是季页的主要耗时。
CREATE INDEX IF NOT EXISTS idx_items_season ON items(season_id);

-- 按分级筛选（ParentalRating）走 official_rating，社区评分已有
-- idx_items_library_rating（community_rating），这是另一个字段。
CREATE INDEX IF NOT EXISTS idx_items_library_official_rating
  ON items(library_id, official_rating);
