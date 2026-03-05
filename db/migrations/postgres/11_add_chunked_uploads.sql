-- +migrate Up
-- Add chunked upload support for large file uploads

-- Upload sessions track the state of a chunked upload
CREATE TABLE IF NOT EXISTS upload_sessions (
    id          TEXT PRIMARY KEY,           -- Session UUID
    pubkey      TEXT NOT NULL,              -- Uploader's public key
    hash        TEXT,                       -- Expected final SHA-256 hash (optional until complete)
    total_size  BIGINT NOT NULL,            -- Expected total size in bytes
    chunk_size  BIGINT NOT NULL,            -- Size of each chunk
    mime_type   TEXT,                       -- Expected MIME type
    chunks_received INTEGER NOT NULL DEFAULT 0,  -- Number of chunks received
    bytes_received  BIGINT NOT NULL DEFAULT 0,  -- Total bytes received
    status      TEXT NOT NULL DEFAULT 'active',  -- active, complete, expired, aborted
    encryption_mode TEXT NOT NULL DEFAULT 'none', -- none, server, e2e
    created     BIGINT NOT NULL,            -- Unix timestamp
    updated     BIGINT NOT NULL,            -- Last update timestamp
    expires_at  BIGINT NOT NULL             -- Expiration timestamp
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_pubkey ON upload_sessions(pubkey);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_status ON upload_sessions(status);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires ON upload_sessions(expires_at);

-- Track individual chunks within a session
CREATE TABLE IF NOT EXISTS upload_chunks (
    session_id  TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    chunk_num   INTEGER NOT NULL,           -- 0-indexed chunk number
    size        BIGINT NOT NULL,            -- Chunk size in bytes
    offset_bytes BIGINT NOT NULL,           -- Byte offset in final file
    hash        TEXT NOT NULL,              -- SHA-256 of this chunk
    received_at BIGINT NOT NULL,            -- Unix timestamp
    PRIMARY KEY (session_id, chunk_num)
);

CREATE INDEX IF NOT EXISTS idx_upload_chunks_session ON upload_chunks(session_id);

-- +migrate Down
DROP TABLE IF EXISTS upload_chunks;
DROP TABLE IF EXISTS upload_sessions;
