-- 片头片尾回填按「没看过 → 在看 → 已完成」排序，需要按 item 查 playstates。
CREATE INDEX IF NOT EXISTS idx_playstates_item
    ON playstates(item_id, played, position_ticks);
