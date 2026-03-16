-- Consolidated schema for sqlc
-- This file is generated from migrations for sqlc to parse correctly

CREATE TABLE IF NOT EXISTS blobs
(
    pubkey          TEXT NOT NULL,
    hash            TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    size            BIGINT NOT NULL,
    blob            BYTEA NOT NULL,
    created         BIGINT NOT NULL,
    encryption_mode TEXT NOT NULL DEFAULT 'none',
    encrypted_dek   TEXT,
    encryption_nonce TEXT,
    original_size   BIGINT,
    ref_count       INTEGER NOT NULL DEFAULT 1,
    expires_at      BIGINT
);

CREATE TABLE IF NOT EXISTS mime_types
(
    extension TEXT PRIMARY KEY,
    mime_type TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users
(
    pubkey      TEXT PRIMARY KEY,
    quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
    used_bytes  BIGINT NOT NULL DEFAULT 0,
    is_banned   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS reports
(
    id              SERIAL PRIMARY KEY,
    reporter_pubkey TEXT,
    blob_hash       TEXT NOT NULL,
    blob_url        TEXT NOT NULL,
    reason          TEXT NOT NULL,
    details         TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    action_taken    TEXT,
    reviewed_by     TEXT,
    created_at      BIGINT NOT NULL,
    reviewed_at     BIGINT
);

CREATE TABLE IF NOT EXISTS blocklist
(
    pubkey      TEXT PRIMARY KEY,
    reason      TEXT NOT NULL,
    blocked_by  TEXT NOT NULL,
    created_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS blob_references
(
    pubkey  TEXT NOT NULL,
    hash    TEXT NOT NULL REFERENCES blobs(hash) ON DELETE CASCADE,
    created BIGINT NOT NULL,
    PRIMARY KEY (pubkey, hash)
);

CREATE TABLE IF NOT EXISTS removed_blobs
(
    hash        TEXT PRIMARY KEY,
    reason      TEXT NOT NULL,
    removed_by  TEXT NOT NULL,
    report_id   INTEGER,
    created_at  BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS upload_sessions (
    id              TEXT PRIMARY KEY,
    pubkey          TEXT NOT NULL,
    hash            TEXT,
    total_size      BIGINT NOT NULL,
    chunk_size      BIGINT NOT NULL,
    mime_type       TEXT,
    chunks_received INTEGER NOT NULL DEFAULT 0,
    bytes_received  BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active',
    encryption_mode TEXT NOT NULL DEFAULT 'none',
    created         BIGINT NOT NULL,
    updated         BIGINT NOT NULL,
    expires_at      BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS upload_chunks (
    session_id  TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    chunk_num   INTEGER NOT NULL,
    size        BIGINT NOT NULL,
    offset_bytes BIGINT NOT NULL,
    hash        TEXT NOT NULL,
    received_at BIGINT NOT NULL,
    PRIMARY KEY (session_id, chunk_num)
);

CREATE TABLE IF NOT EXISTS expiration_policies (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    mime_prefix TEXT,
    ttl_seconds INTEGER NOT NULL,
    max_size    BIGINT,
    pubkey      TEXT,
    priority    INTEGER NOT NULL DEFAULT 0,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

-- Transparency stats table for moderation reporting
CREATE TABLE IF NOT EXISTS transparency_stats (
    id                SERIAL PRIMARY KEY,
    total_reports     BIGINT NOT NULL DEFAULT 0,
    reports_actioned  BIGINT NOT NULL DEFAULT 0,
    reports_dismissed BIGINT NOT NULL DEFAULT 0,
    blobs_removed     BIGINT NOT NULL DEFAULT 0,
    users_banned      BIGINT NOT NULL DEFAULT 0,
    last_updated      BIGINT NOT NULL
);

-- Federation tables

-- Federated blobs discovered via kind 1063 events
CREATE TABLE IF NOT EXISTS federated_blobs (
    hash            TEXT PRIMARY KEY,
    size            BIGINT NOT NULL,
    mime_type       TEXT NOT NULL,
    ref_count       INTEGER NOT NULL DEFAULT 1,
    status          TEXT NOT NULL DEFAULT 'discovered',
    discovered_at   BIGINT NOT NULL,
    mirrored_at     BIGINT,
    last_seen_at    BIGINT NOT NULL
);

-- Remote URLs for federated blobs
CREATE TABLE IF NOT EXISTS federated_blob_urls (
    id          SERIAL PRIMARY KEY,
    blob_hash   TEXT NOT NULL REFERENCES federated_blobs(hash) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    server_id   TEXT,
    priority    INTEGER NOT NULL DEFAULT 0,
    healthy     BOOLEAN NOT NULL DEFAULT TRUE,
    last_check  BIGINT,
    created_at  BIGINT NOT NULL,
    UNIQUE(blob_hash, url)
);

-- Known Blossom servers discovered via kind 10063 events
CREATE TABLE IF NOT EXISTS known_servers (
    url         TEXT PRIMARY KEY,
    pubkey      TEXT,
    user_count  INTEGER NOT NULL DEFAULT 0,
    blob_count  INTEGER NOT NULL DEFAULT 0,
    healthy     BOOLEAN NOT NULL DEFAULT TRUE,
    first_seen  BIGINT NOT NULL,
    last_seen   BIGINT NOT NULL,
    last_check  BIGINT
);

-- Federation events (published and received)
CREATE TABLE IF NOT EXISTS federation_events (
    id          TEXT PRIMARY KEY,
    event_id    TEXT,
    event_kind  INTEGER NOT NULL,
    pubkey      TEXT NOT NULL,
    blob_hash   TEXT,
    direction   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    error       TEXT,
    relay_url   TEXT,
    created_at  BIGINT NOT NULL,
    published_at BIGINT,
    retries     INTEGER NOT NULL DEFAULT 0
);

-- Users who have this server in their kind 10063 server list
CREATE TABLE IF NOT EXISTS federated_users (
    pubkey      TEXT PRIMARY KEY,
    event_id    TEXT NOT NULL,
    server_rank INTEGER NOT NULL DEFAULT 0,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
