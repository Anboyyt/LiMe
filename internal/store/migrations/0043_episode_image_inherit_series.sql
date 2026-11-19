-- 单集不再单独刮削剧照，封面统一继承剧集大海报。
-- 只改刮削下载的图（images/ 下、且不是面板手传的 <id>-primary-*）；
-- local: 本地海报和用户自己上传的封面都保留。
UPDATE items
   SET image_path = (SELECT s.image_path FROM items s WHERE s.id = items.series_id)
 WHERE type = 'Episode'
   AND series_id <> ''
   AND image_path LIKE 'images/%'
   AND image_path NOT LIKE '%' || id || '-primary-%'
   AND EXISTS (
     SELECT 1 FROM items s
      WHERE s.id = items.series_id
        AND s.image_path <> ''
   )
   AND image_path <> (SELECT s.image_path FROM items s WHERE s.id = items.series_id);
