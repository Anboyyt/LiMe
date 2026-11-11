-- 记录各类「补缺」任务最近一次尝试的时间。
-- TMDB 本身没有评分/类型/时长/剧照时，旧逻辑每天都会重新请求一遍；
-- 有了 checked_at，同一项在间隔内不再重复，到期后再试一次。
ALTER TABLE items ADD COLUMN genres_checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN rating_checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN runtime_checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN episode_stills_checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN episode_runtime_checked_at INTEGER NOT NULL DEFAULT 0;
