-- +migrate Up
-- Resumable (tus) uploads in progress. Every replica must see an upload, so
-- its state lives here and each chunk's bytes are an object in blob storage.
CREATE TABLE IF NOT EXISTS tus_uploads (
    id            TEXT PRIMARY KEY,         -- Upload UUID (the capability in the URL)
    pubkey        TEXT NOT NULL,            -- Key that created the upload
    size          BIGINT NOT NULL,          -- Declared Upload-Length
    upload_offset BIGINT NOT NULL DEFAULT 0, -- Bytes received so far
    metadata      TEXT NOT NULL DEFAULT '{}', -- Upload-Metadata, as JSON
    created_at    BIGINT NOT NULL,          -- Unix timestamp
    expires_at    BIGINT NOT NULL           -- Unix timestamp
);

CREATE INDEX IF NOT EXISTS idx_tus_uploads_expires ON tus_uploads(expires_at);

-- One row per stored chunk; object_key names its bytes in blob storage.
CREATE TABLE IF NOT EXISTS tus_upload_chunks (
    upload_id    TEXT NOT NULL,
    chunk_offset BIGINT NOT NULL,           -- Byte offset of the chunk in the upload
    size         BIGINT NOT NULL,
    object_key   TEXT NOT NULL,
    PRIMARY KEY (upload_id, chunk_offset)
);

-- +migrate Down
DROP TABLE IF EXISTS tus_upload_chunks;
DROP TABLE IF EXISTS tus_uploads;
