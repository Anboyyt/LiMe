-- 片头片尾回填改为只处理最近在看（>=10 分钟）的剧。
-- 旧配置的 watching 会包含全部收藏，导致一上来就回填所有老剧。
UPDATE settings SET value = 'active'
WHERE key = 'segment_scope' AND value = 'watching';
