-- +migrate Up
-- Performance indexes for common query patterns

-- Index on blobs.type for MIME type prefix filtering (e.g., type LIKE 'image/%')
CREATE INDEX IF NOT EXISTS idx_blobs_type ON blobs(type);

-- Index on blobs.created for date range queries (since/until filters)
CREATE INDEX IF NOT EXISTS idx_blobs_created ON blobs(created);

-- Index on blobs.pubkey for legacy direct pubkey queries
CREATE INDEX IF NOT EXISTS idx_blobs_pubkey ON blobs(pubkey);

-- Composite index on blob_references for efficient sorted listing by pubkey
-- This speeds up queries like: SELECT ... FROM blob_references WHERE pubkey = ? ORDER BY created
CREATE INDEX IF NOT EXISTS idx_blob_references_pubkey_created ON blob_references(pubkey, created);

-- +migrate Down
DROP INDEX IF EXISTS idx_blob_references_pubkey_created;
DROP INDEX IF EXISTS idx_blobs_pubkey;
DROP INDEX IF EXISTS idx_blobs_created;
DROP INDEX IF EXISTS idx_blobs_type;
