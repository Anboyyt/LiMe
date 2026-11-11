-- 筛选、续播、会话查询的常用索引
CREATE INDEX IF NOT EXISTS idx_items_production_year ON items(production_year);
CREATE INDEX IF NOT EXISTS idx_items_community_rating ON items(community_rating);
CREATE INDEX IF NOT EXISTS idx_items_library_year ON items(library_id, production_year);
CREATE INDEX IF NOT EXISTS idx_items_library_rating ON items(library_id, community_rating);
CREATE INDEX IF NOT EXISTS idx_playstates_user_played ON playstates(user_id, played, updated_at);
CREATE INDEX IF NOT EXISTS idx_playstates_user_updated ON playstates(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_playback_sessions_updated ON playback_sessions(updated_at);
