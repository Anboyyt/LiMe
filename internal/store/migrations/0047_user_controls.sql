-- 用户管理增强：并发播放上限、下载/播放开关。
-- max_active_sessions = 0 表示不限制；allow_* 默认放开，保持老账号行为不变。
ALTER TABLE users ADD COLUMN max_active_sessions INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN allow_download INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN allow_playback INTEGER NOT NULL DEFAULT 1;

-- 设备记录里补一个最后访问 IP，设备管理页要显示来源。
ALTER TABLE devices ADD COLUMN last_ip TEXT NOT NULL DEFAULT '';
