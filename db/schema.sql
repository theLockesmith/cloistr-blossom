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
