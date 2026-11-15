-- 大库瓶颈索引：
-- image_path 没有索引时，每次替换海报都要全表扫一遍（CountPosterRefs）。
CREATE INDEX IF NOT EXISTS idx_items_image_path
    ON items(image_path) WHERE image_path <> '';

-- 刮削队列按 type + scraped_at=0 反复查询；部分索引只存未刮削条目。
CREATE INDEX IF NOT EXISTS idx_items_scrape_queue
    ON items(type, scraped_at) WHERE scraped_at = 0;

-- 全量扫描后清理 stale items：WHERE library_id=? AND updated_at<?
CREATE INDEX IF NOT EXISTS idx_items_library_updated
    ON items(library_id, updated_at);
