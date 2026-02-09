-- +migrate Up
-- Add encryption support to blobs table
-- encryption_mode: 'none' (plaintext), 'server' (server-side encryption), 'e2e' (end-to-end, client encrypted)
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS encryption_mode TEXT NOT NULL DEFAULT 'none';

-- Encrypted DEK (Data Encryption Key) - base64 encoded, encrypted with master KEK
-- Only populated for 'server' mode
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS encrypted_dek TEXT;

-- Encryption nonce/IV - base64 encoded, 12 bytes for AES-GCM
-- Only populated for 'server' mode
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS encryption_nonce TEXT;

-- Original size before encryption (encrypted blobs have auth tag overhead)
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS original_size BIGINT;

CREATE INDEX IF NOT EXISTS idx_blobs_encryption_mode ON blobs(encryption_mode);

-- +migrate Down
DROP INDEX IF EXISTS idx_blobs_encryption_mode;
ALTER TABLE blobs DROP COLUMN IF EXISTS original_size;
ALTER TABLE blobs DROP COLUMN IF EXISTS encryption_nonce;
ALTER TABLE blobs DROP COLUMN IF EXISTS encrypted_dek;
ALTER TABLE blobs DROP COLUMN IF EXISTS encryption_mode;
