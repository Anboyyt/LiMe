-- 演职人员索引：刮削时把 items.people 里的名字展开成可反查的记录。
-- 演员 ID 由名字哈希得到，无法从 ID 反推名字，所以必须单独存一份。
CREATE TABLE IF NOT EXISTS persons (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    image_url  TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_persons_name ON persons(name COLLATE NOCASE);

-- 老库里已有的头像记录直接搬过来，避免升级后头像 404。
INSERT OR IGNORE INTO persons (id, name, image_url, updated_at)
SELECT id, name, image_url, updated_at FROM person_images;
