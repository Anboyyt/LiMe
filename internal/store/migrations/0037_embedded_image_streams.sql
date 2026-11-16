-- MKV 内嵌封面（mjpeg/png 等、没有帧率）之前被存成 Video。
-- 部分客户端（Yamby/ExoPlayer）会因此判定「有视频轨道，但都不可播放」。
-- 存量数据一次性纠正；新探测在 convert() 里直接写成 EmbeddedImage。
UPDATE media_streams
SET type = 'EmbeddedImage'
WHERE type = 'Video'
  AND lower(codec) IN ('mjpeg', 'png', 'bmp', 'gif', 'webp')
  AND frame_rate = 0;
