-- 媒体库自定义排序
ALTER TABLE libraries ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

-- 初始顺序按名称，之后由面板调整。
UPDATE libraries SET sort_order = (
    SELECT COUNT(*) FROM libraries l2 WHERE l2.name < libraries.name
);
