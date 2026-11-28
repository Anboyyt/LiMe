package store

import "time"

// FeaturedItems 返回首页精选候选：TMDB 评分占 75%，近 30 天播放次数占 25%。
// 个人库里播放记录稀疏，热度权重不宜过高；没有播放记录的条目靠评分也能上榜。
func (d *DB) FeaturedItems(limit int) ([]*Item, error) {
	if limit <= 0 || limit > 50 {
		limit = 8
	}
	since := time.Now().AddDate(0, 0, -30).Unix()
	rows, err := d.Query(`
SELECT `+itemColumns+`
FROM items i
LEFT JOIN (
  -- 只数 start 事件：pause/unpause/stop 也会入库，全算会把暂停次数当热度。
  SELECT item_id, COUNT(*) AS c
  FROM playback_history
  WHERE created_at > ? AND event = 'start'
  GROUP BY item_id
) h ON h.item_id = i.id
WHERE i.type IN (?, ?)
  AND (COALESCE(i.image_path, '') <> '' OR COALESCE(i.backdrop_path, '') <> '')
-- 评分是主排序，热度最多加 0.5 分（播 10 次封顶）：
-- 只用来在同分段里把常看的往前提，不能跨过 0.5 分以上的评分差。
ORDER BY (
    COALESCE(i.community_rating, 0)
  + MIN(COALESCE(h.c, 0), 10) * 0.05
) DESC, COALESCE(i.community_rating, 0) DESC, i.sort_name
LIMIT ?`, since, TypeMovie, TypeSeries, limit)
	if err != nil {
		return nil, err
	}
	return scanItemsLimited(rows, limit)
}
