-- +migrate Up
-- Remote media mirror (custom emoji and other third-party images).
--
-- One row per REMOTE URL, not per request and not per viewer. The mirror has
-- to remember which URLs it has fetched in order to cache them; it must never
-- remember who asked. There is deliberately no pubkey, IP, or per-request
-- column anywhere in this table -- adding one would turn a privacy feature
-- into a surveillance log.
--
-- Failures are rows too. A dead host or a refused image is cached as such,
-- with the reason, so that (a) we do not re-fetch a dead host on every page
-- view and (b) the client can tell "this content was refused" from "this host
-- is unreachable" instead of rendering both as a broken image.
CREATE TABLE IF NOT EXISTS mirrored_media (
    -- SHA-256 of the canonical source URL. Primary key rather than the URL
    -- itself so the key is fixed-width and index-friendly.
    url_hash     TEXT PRIMARY KEY,
    -- The canonical source URL, kept for operability: moderation takedowns and
    -- debugging both need to know what a cached object actually is. Not
    -- personal data -- it identifies content, not a person.
    source_url   TEXT NOT NULL,
    -- 'ok' | 'refused' | 'unreachable'
    status       TEXT NOT NULL,
    -- Machine-readable cause when status is not 'ok' (e.g. 'too_large').
    reason       TEXT,
    -- Content hash of the mirrored bytes; joins to blobs.hash. NULL unless ok.
    sha256       TEXT,
    -- Byte size of the mirrored object; 0 for failures. Denormalized from
    -- blobs so the eviction sweep can total the cache without a join.
    size         INTEGER NOT NULL DEFAULT 0,
    mime         TEXT,
    fetched_at   INTEGER NOT NULL,
    -- Drives LRU eviction. Stored coarsely (the service rounds it) so it is a
    -- popularity signal rather than a precise access-time record, and so a hot
    -- object does not cause a database write per request.
    accessed_at  INTEGER NOT NULL
);

-- Eviction scans oldest-accessed first among successfully mirrored entries.
CREATE INDEX IF NOT EXISTS idx_mirrored_media_lru
    ON mirrored_media(accessed_at)
    WHERE status = 'ok';

-- Negative-cache expiry scans by age within a status.
CREATE INDEX IF NOT EXISTS idx_mirrored_media_status_fetched
    ON mirrored_media(status, fetched_at);

-- Reverse lookup: which URL(s) produced a given blob. Several distinct URLs
-- can serve identical bytes, so this is not unique.
CREATE INDEX IF NOT EXISTS idx_mirrored_media_sha256
    ON mirrored_media(sha256)
    WHERE sha256 IS NOT NULL;

-- +migrate Down
DROP INDEX IF EXISTS idx_mirrored_media_sha256;
DROP INDEX IF EXISTS idx_mirrored_media_status_fetched;
DROP INDEX IF EXISTS idx_mirrored_media_lru;
DROP TABLE IF EXISTS mirrored_media;
