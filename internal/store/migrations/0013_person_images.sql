-- 演员头像：刮削时只记录 TMDB 图片地址，客户端请求时再按需下载缓存
CREATE TABLE IF NOT EXISTS person_images (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    image_url  TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_person_images_name ON person_images(name);
