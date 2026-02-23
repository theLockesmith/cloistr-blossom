-- +migrate Up
-- Add deduplication support: allow multiple users to reference the same blob

-- Create blob_references table for user-to-blob mapping
-- This enables content-addressable deduplication across users
CREATE TABLE IF NOT EXISTS blob_references (
    pubkey  TEXT NOT NULL,
    hash    TEXT NOT NULL REFERENCES blobs(hash) ON DELETE CASCADE,
    created INTEGER NOT NULL,
    PRIMARY KEY (pubkey, hash)
);

CREATE INDEX IF NOT EXISTS idx_blob_references_hash ON blob_references(hash);
CREATE INDEX IF NOT EXISTS idx_blob_references_pubkey ON blob_references(pubkey);

-- Add reference count to blobs table for efficient deletion decisions
ALTER TABLE blobs ADD COLUMN ref_count INTEGER NOT NULL DEFAULT 1;

-- Migrate existing data: create references from existing blobs
INSERT INTO blob_references (pubkey, hash, created)
SELECT pubkey, hash, created
FROM blobs;

-- +migrate Down
DROP TABLE IF EXISTS blob_references;
-- SQLite doesn't support DROP COLUMN easily, so we skip it for sqlite migration
