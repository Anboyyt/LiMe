-- ffprobe 探测到的时长之前只写在 media_sources.duration_ticks，
-- 没有回填 items.run_time_ticks，导致客户端拿不到时长、画不出进度条
-- （/Items/Resume 里看着像「没有播放记录」），95% 完成判定也永远不触发。
-- 新增的探测会走 FillItemRuntimeFromSources 自动回填，这里把存量一次性补上。
-- 只补空值，不覆盖刮削出来的时长；多版本取最大值。

UPDATE items SET run_time_ticks = (
  SELECT MAX(ms.duration_ticks) FROM media_sources ms
  WHERE ms.item_id = items.id AND ms.probe_status = 'done' AND ms.duration_ticks > 0
)
WHERE run_time_ticks = 0
  AND EXISTS (
    SELECT 1 FROM media_sources ms
    WHERE ms.item_id = items.id AND ms.probe_status = 'done' AND ms.duration_ticks > 0
  );
