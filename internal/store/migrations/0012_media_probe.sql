-- ffprobe 探测出的完整媒体信息
ALTER TABLE media_streams ADD COLUMN bit_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_streams ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN level REAL NOT NULL DEFAULT 0;
ALTER TABLE media_streams ADD COLUMN aspect_ratio TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN is_interlaced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_streams ADD COLUMN color_primaries TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN color_transfer TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN color_space TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN pixel_format TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_streams ADD COLUMN channel_layout TEXT NOT NULL DEFAULT '';
ALTER TABLE media_streams ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_streams ADD COLUMN is_forced INTEGER NOT NULL DEFAULT 0;
