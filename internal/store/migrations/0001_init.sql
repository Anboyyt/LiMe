-- LiMe 初始 schema

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER
);

CREATE TABLE IF NOT EXISTS devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id    TEXT NOT NULL,
    client       TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    last_seen_at INTEGER,
    UNIQUE (user_id, device_id)
);

CREATE TABLE IF NOT EXISTS access_tokens (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_access_tokens_user ON access_tokens(user_id);

CREATE TABLE IF NOT EXISTS libraries (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    path         TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT 'mixed',
    mode         TEXT NOT NULL DEFAULT 'redirect',
    rewrite_host TEXT,
    created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
    id                  TEXT PRIMARY KEY,
    library_id          TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    parent_id           TEXT NOT NULL DEFAULT '',
    type                TEXT NOT NULL,
    name                TEXT NOT NULL,
    original_title      TEXT NOT NULL DEFAULT '',
    sort_name           TEXT NOT NULL DEFAULT '',
    overview            TEXT NOT NULL DEFAULT '',
    production_year     INTEGER NOT NULL DEFAULT 0,
    index_number        INTEGER NOT NULL DEFAULT 0,
    parent_index_number INTEGER NOT NULL DEFAULT 0,
    series_id           TEXT,
    season_id           TEXT,
    tmdb_id             TEXT NOT NULL DEFAULT '',
    run_time_ticks      INTEGER NOT NULL DEFAULT 0,
    official_rating     TEXT NOT NULL DEFAULT '',
    community_rating    REAL NOT NULL DEFAULT 0,
    premiere_date       TEXT NOT NULL DEFAULT '',
    image_path          TEXT NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_library ON items(library_id);
CREATE INDEX IF NOT EXISTS idx_items_parent ON items(parent_id);
CREATE INDEX IF NOT EXISTS idx_items_type ON items(type);
CREATE INDEX IF NOT EXISTS idx_items_series ON items(series_id);
CREATE INDEX IF NOT EXISTS idx_items_sort ON items(sort_name);

CREATE TABLE IF NOT EXISTS media_sources (
    id             TEXT PRIMARY KEY,
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    strm_path      TEXT NOT NULL DEFAULT '',
    url            TEXT NOT NULL,
    cloud          TEXT NOT NULL DEFAULT '',
    drive_id       TEXT NOT NULL DEFAULT '',
    cloud_file_id  TEXT NOT NULL DEFAULT '',
    sha1_etag      TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    container      TEXT NOT NULL DEFAULT '',
    file_name      TEXT NOT NULL DEFAULT '',
    duration_ticks INTEGER NOT NULL DEFAULT 0,
    probe_status   TEXT NOT NULL DEFAULT 'none',
    strm_mtime     INTEGER NOT NULL DEFAULT 0,
    strm_size      INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_media_sources_item ON media_sources(item_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_sources_cloud
    ON media_sources(drive_id, cloud_file_id) WHERE cloud_file_id <> '';

CREATE TABLE IF NOT EXISTS media_streams (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id     TEXT NOT NULL REFERENCES media_sources(id) ON DELETE CASCADE,
    stream_index  INTEGER NOT NULL,
    type          TEXT NOT NULL,
    codec         TEXT NOT NULL DEFAULT '',
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    bit_depth     INTEGER NOT NULL DEFAULT 0,
    channels      INTEGER NOT NULL DEFAULT 0,
    frame_rate    REAL NOT NULL DEFAULT 0,
    language      TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    video_range   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_media_streams_source ON media_streams(source_id);

CREATE TABLE IF NOT EXISTS playstates (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position_ticks INTEGER NOT NULL DEFAULT 0,
    played         INTEGER NOT NULL DEFAULT 0,
    is_favorite    INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

CREATE TABLE IF NOT EXISTS playback_sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id    TEXT NOT NULL DEFAULT '',
    item_id      TEXT NOT NULL DEFAULT '',
    position_ticks INTEGER NOT NULL DEFAULT 0,
    is_paused    INTEGER NOT NULL DEFAULT 0,
    started_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
