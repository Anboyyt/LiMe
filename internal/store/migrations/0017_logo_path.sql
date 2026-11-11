-- ClearLogo：TMDB images.logos 里的透明标题图，作为 ImageType.Logo 给客户端。
ALTER TABLE items ADD COLUMN logo_path TEXT NOT NULL DEFAULT '';

-- 是否已尝试过刮 logo。TMDB 没有 logo 也算已尝试，避免每日刮削反复请求。
ALTER TABLE items ADD COLUMN logo_checked INTEGER NOT NULL DEFAULT 0;
