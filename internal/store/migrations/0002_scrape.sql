-- M4：TMDB 刮削
-- image_path 存海报（相对 data 目录），backdrop_path 存背景图
ALTER TABLE items ADD COLUMN backdrop_path TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN scraped_at INTEGER NOT NULL DEFAULT 0;
