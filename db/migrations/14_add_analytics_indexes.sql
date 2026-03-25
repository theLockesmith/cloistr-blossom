-- +migrate Up
-- Indexes for analytics queries

-- Index on blob_references.created for time-series analytics
-- (distinct from the composite pubkey,created index)
CREATE INDEX IF NOT EXISTS idx_blob_references_created ON blob_references(created);

-- Index on users.created_at for user growth analytics
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at);

-- Index on users.used_bytes for top users query (descending order)
CREATE INDEX IF NOT EXISTS idx_users_used_bytes_desc ON users(used_bytes DESC) WHERE used_bytes > 0;

-- Partial index on blobs.encryption_mode for encryption stats
CREATE INDEX IF NOT EXISTS idx_blobs_encryption_mode ON blobs(encryption_mode) WHERE encryption_mode IS NOT NULL AND encryption_mode != '';

-- +migrate Down
DROP INDEX IF EXISTS idx_blobs_encryption_mode;
DROP INDEX IF EXISTS idx_users_used_bytes_desc;
DROP INDEX IF EXISTS idx_users_created_at;
DROP INDEX IF EXISTS idx_blob_references_created;
