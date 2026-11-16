-- 0037 只处理了 frame_rate=0 的封面；ffprobe 对 attached picture
-- 常给 avg_frame_rate=90000/1，这里补齐。真实视频不会有这么高的帧率。
UPDATE media_streams
SET type = 'EmbeddedImage'
WHERE type = 'Video'
  AND lower(codec) IN ('mjpeg', 'png', 'bmp', 'gif', 'webp')
  AND (frame_rate = 0 OR frame_rate >= 1000);
