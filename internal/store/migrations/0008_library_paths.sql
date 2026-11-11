-- 一个媒体库支持多个文件夹路径。
CREATE TABLE IF NOT EXISTS library_paths (
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    PRIMARY KEY (library_id, path)
);

INSERT OR IGNORE INTO library_paths (library_id, path)
SELECT id, path FROM libraries WHERE path <> '';
