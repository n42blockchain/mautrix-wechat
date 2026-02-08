-- 0001_initial_schema.sql
-- Initial database schema for mautrix-wechat bridge

-- WeChat user to Matrix puppet mapping
CREATE TABLE IF NOT EXISTS wechat_user (
    wechat_id        TEXT PRIMARY KEY,
    alias            TEXT,
    nickname         TEXT NOT NULL,
    avatar_url       TEXT,
    avatar_mxc       TEXT,
    gender           SMALLINT DEFAULT 0,
    province         TEXT,
    city             TEXT,
    signature        TEXT,
    matrix_user_id   TEXT UNIQUE NOT NULL,
    name_set         BOOLEAN DEFAULT FALSE,
    avatar_set       BOOLEAN DEFAULT FALSE,
    contact_info_set BOOLEAN DEFAULT FALSE,
    last_sync        TIMESTAMP WITH TIME ZONE,
    created_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Bridge users (real Matrix users who use the bridge)
CREATE TABLE IF NOT EXISTS bridge_user (
    matrix_user_id  TEXT PRIMARY KEY,
    wechat_id       TEXT,
    provider_type   TEXT NOT NULL,
    login_state     SMALLINT DEFAULT 0,
    management_room TEXT,
    space_room      TEXT,
    last_login      TIMESTAMP WITH TIME ZONE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Room mapping: WeChat chat <-> Matrix room
CREATE TABLE IF NOT EXISTS room_mapping (
    wechat_chat_id  TEXT NOT NULL,
    matrix_room_id  TEXT NOT NULL UNIQUE,
    bridge_user     TEXT NOT NULL REFERENCES bridge_user(matrix_user_id),
    is_group        BOOLEAN DEFAULT FALSE,
    name            TEXT,
    avatar_mxc      TEXT,
    topic           TEXT,
    encrypted       BOOLEAN DEFAULT FALSE,
    name_set        BOOLEAN DEFAULT FALSE,
    avatar_set      BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (wechat_chat_id, bridge_user)
);
CREATE INDEX IF NOT EXISTS idx_room_mapping_matrix ON room_mapping(matrix_room_id);

-- Message mapping: WeChat message ID <-> Matrix event ID
CREATE TABLE IF NOT EXISTS message_mapping (
    wechat_msg_id   TEXT NOT NULL,
    matrix_event_id TEXT NOT NULL,
    matrix_room_id  TEXT NOT NULL,
    sender          TEXT NOT NULL,
    msg_type        SMALLINT NOT NULL,
    timestamp       TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (wechat_msg_id, matrix_room_id)
);
CREATE INDEX IF NOT EXISTS idx_msg_mapping_event ON message_mapping(matrix_event_id);

-- Group members
CREATE TABLE IF NOT EXISTS group_member (
    group_id     TEXT NOT NULL,
    wechat_id    TEXT NOT NULL,
    display_name TEXT,
    is_admin     BOOLEAN DEFAULT FALSE,
    is_owner     BOOLEAN DEFAULT FALSE,
    joined_at    TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (group_id, wechat_id)
);

-- Media cache: WeChat media ID -> Matrix MXC URI
CREATE TABLE IF NOT EXISTS media_cache (
    wechat_media_id TEXT PRIMARY KEY,
    matrix_mxc      TEXT NOT NULL,
    mime_type       TEXT,
    file_size       BIGINT,
    file_name       TEXT,
    cached_at       TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Provider session persistence
CREATE TABLE IF NOT EXISTS provider_session (
    bridge_user   TEXT PRIMARY KEY REFERENCES bridge_user(matrix_user_id),
    provider_type TEXT NOT NULL,
    session_data  JSONB NOT NULL,
    cookies       BYTEA,
    device_info   JSONB,
    updated_at    TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Audit log
CREATE TABLE IF NOT EXISTS bridge_audit_log (
    id            BIGSERIAL PRIMARY KEY,
    bridge_user   TEXT,
    action        TEXT NOT NULL,
    provider_type TEXT,
    details       JSONB,
    ip_address    INET,
    created_at    TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Rate limiting counters
CREATE TABLE IF NOT EXISTS rate_limit (
    bridge_user    TEXT NOT NULL,
    window_start   TIMESTAMP WITH TIME ZONE NOT NULL,
    message_count  INT DEFAULT 0,
    media_count    INT DEFAULT 0,
    api_call_count INT DEFAULT 0,
    PRIMARY KEY (bridge_user, window_start)
);
