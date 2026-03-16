-- +migrate Up
-- Add federation support for Nostr-based cross-server blob discovery

-- Federated blobs discovered via kind 1063 events
CREATE TABLE IF NOT EXISTS federated_blobs (
    hash            TEXT PRIMARY KEY,              -- Blob SHA-256 hash
    size            BIGINT NOT NULL,               -- Blob size in bytes
    mime_type       TEXT NOT NULL,                 -- MIME type
    ref_count       INTEGER NOT NULL DEFAULT 1,    -- Number of kind 1063 events referencing this blob
    status          TEXT NOT NULL DEFAULT 'discovered', -- discovered, mirroring, mirrored, failed
    discovered_at   BIGINT NOT NULL,               -- Unix timestamp when first seen
    mirrored_at     BIGINT,                        -- Unix timestamp when mirrored locally (NULL if not mirrored)
    last_seen_at    BIGINT NOT NULL                -- Unix timestamp of most recent kind 1063 event
);

CREATE INDEX IF NOT EXISTS idx_federated_blobs_status ON federated_blobs(status);
CREATE INDEX IF NOT EXISTS idx_federated_blobs_ref_count ON federated_blobs(ref_count DESC);
CREATE INDEX IF NOT EXISTS idx_federated_blobs_discovered ON federated_blobs(discovered_at DESC);

-- Remote URLs for federated blobs
CREATE TABLE IF NOT EXISTS federated_blob_urls (
    id          SERIAL PRIMARY KEY,
    blob_hash   TEXT NOT NULL REFERENCES federated_blobs(hash) ON DELETE CASCADE,
    url         TEXT NOT NULL,                     -- Remote URL where blob is available
    server_id   TEXT,                              -- Server pubkey or URL identifier
    priority    INTEGER NOT NULL DEFAULT 0,        -- Lower = higher priority
    healthy     BOOLEAN NOT NULL DEFAULT TRUE,     -- Whether this URL is reachable
    last_check  BIGINT,                            -- Unix timestamp of last health check
    created_at  BIGINT NOT NULL,
    UNIQUE(blob_hash, url)
);

CREATE INDEX IF NOT EXISTS idx_federated_blob_urls_hash ON federated_blob_urls(blob_hash);
CREATE INDEX IF NOT EXISTS idx_federated_blob_urls_healthy ON federated_blob_urls(blob_hash, healthy, priority);

-- Known Blossom servers discovered via kind 10063 events
CREATE TABLE IF NOT EXISTS known_servers (
    url         TEXT PRIMARY KEY,                  -- Server URL
    pubkey      TEXT,                              -- Server's Nostr pubkey if known
    user_count  INTEGER NOT NULL DEFAULT 0,        -- Users with this server in kind 10063
    blob_count  INTEGER NOT NULL DEFAULT 0,        -- Blobs discovered from this server
    healthy     BOOLEAN NOT NULL DEFAULT TRUE,
    first_seen  BIGINT NOT NULL,
    last_seen   BIGINT NOT NULL,
    last_check  BIGINT                             -- Last health check timestamp
);

CREATE INDEX IF NOT EXISTS idx_known_servers_healthy ON known_servers(healthy);
CREATE INDEX IF NOT EXISTS idx_known_servers_user_count ON known_servers(user_count DESC);

-- Federation events (published and received)
CREATE TABLE IF NOT EXISTS federation_events (
    id          TEXT PRIMARY KEY,                  -- Internal ID (UUID)
    event_id    TEXT,                              -- Nostr event ID (set after publish)
    event_kind  INTEGER NOT NULL,                  -- Nostr event kind (1063 or 10063)
    pubkey      TEXT NOT NULL,                     -- Event author pubkey
    blob_hash   TEXT,                              -- Associated blob hash (for kind 1063)
    direction   TEXT NOT NULL,                     -- 'publish' or 'receive'
    status      TEXT NOT NULL DEFAULT 'pending',   -- pending, published, failed, received
    error       TEXT,                              -- Error message if failed
    relay_url   TEXT,                              -- Relay URL for this event
    created_at  BIGINT NOT NULL,
    published_at BIGINT,                           -- When event was published (NULL if pending/failed)
    retries     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_federation_events_status ON federation_events(direction, status);
CREATE INDEX IF NOT EXISTS idx_federation_events_blob ON federation_events(blob_hash);
CREATE INDEX IF NOT EXISTS idx_federation_events_created ON federation_events(created_at DESC);

-- Users who have this server in their kind 10063 server list
CREATE TABLE IF NOT EXISTS federated_users (
    pubkey      TEXT PRIMARY KEY,                  -- User's Nostr pubkey
    event_id    TEXT NOT NULL,                     -- kind 10063 event ID
    server_rank INTEGER NOT NULL DEFAULT 0,        -- Position in user's server list (0 = primary)
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_federated_users_rank ON federated_users(server_rank);

-- +migrate Down
DROP TABLE IF EXISTS federated_users;
DROP TABLE IF EXISTS federation_events;
DROP TABLE IF EXISTS known_servers;
DROP TABLE IF EXISTS federated_blob_urls;
DROP TABLE IF EXISTS federated_blobs;
