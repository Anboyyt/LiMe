-- 多版本片源的展示名和默认排序
ALTER TABLE media_sources ADD COLUMN version_name TEXT NOT NULL DEFAULT '';
ALTER TABLE media_sources ADD COLUMN sort_rank INTEGER NOT NULL DEFAULT 0;
