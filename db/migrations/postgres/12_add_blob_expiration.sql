-- +migrate Up
-- Add blob expiration support for auto-delete policies

-- Add expiration columns to blobs table
ALTER TABLE blobs ADD COLUMN IF NOT EXISTS expires_at BIGINT;  -- Unix timestamp when blob expires (NULL = never)

-- Index for efficient expiration queries
CREATE INDEX IF NOT EXISTS idx_blobs_expires ON blobs(expires_at) WHERE expires_at IS NOT NULL;

-- Expiration policies table for configurable TTL rules
CREATE TABLE IF NOT EXISTS expiration_policies (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,              -- Policy name (e.g., "default", "temp", "archive")
    mime_prefix TEXT,                              -- MIME type prefix to match (NULL = all types)
    ttl_seconds INTEGER NOT NULL,                  -- TTL in seconds
    max_size    BIGINT,                            -- Only apply to blobs under this size (NULL = no limit)
    pubkey      TEXT,                              -- Only apply to specific pubkey (NULL = all users)
    priority    INTEGER NOT NULL DEFAULT 0,        -- Higher priority policies take precedence
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_expiration_policies_enabled ON expiration_policies(enabled, priority DESC);

-- Insert default policies (disabled by default)
INSERT INTO expiration_policies (name, mime_prefix, ttl_seconds, priority, enabled, created_at, updated_at)
VALUES
    ('temp-uploads', NULL, 86400, 0, FALSE, EXTRACT(EPOCH FROM NOW())::BIGINT, EXTRACT(EPOCH FROM NOW())::BIGINT),
    ('temp-images', 'image/', 604800, 1, FALSE, EXTRACT(EPOCH FROM NOW())::BIGINT, EXTRACT(EPOCH FROM NOW())::BIGINT),
    ('temp-videos', 'video/', 2592000, 2, FALSE, EXTRACT(EPOCH FROM NOW())::BIGINT, EXTRACT(EPOCH FROM NOW())::BIGINT);

-- +migrate Down
ALTER TABLE blobs DROP COLUMN expires_at;
DROP TABLE IF EXISTS expiration_policies;
