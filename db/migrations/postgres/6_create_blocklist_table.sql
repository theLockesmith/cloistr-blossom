-- +migrate Up
CREATE TABLE IF NOT EXISTS blocklist
(
    pubkey      TEXT PRIMARY KEY,
    reason      TEXT NOT NULL,
    blocked_by  TEXT NOT NULL,              -- Admin pubkey who blocked
    created_at  BIGINT NOT NULL
);

-- Transparency stats table for caching aggregated stats
CREATE TABLE IF NOT EXISTS transparency_stats
(
    id                  INTEGER PRIMARY KEY DEFAULT 1,
    total_reports       BIGINT NOT NULL DEFAULT 0,
    reports_actioned    BIGINT NOT NULL DEFAULT 0,
    reports_dismissed   BIGINT NOT NULL DEFAULT 0,
    blobs_removed       BIGINT NOT NULL DEFAULT 0,
    users_banned        BIGINT NOT NULL DEFAULT 0,
    last_updated        BIGINT NOT NULL,
    CONSTRAINT single_row CHECK (id = 1)
);

-- Initialize with zeros
INSERT INTO transparency_stats (id, total_reports, reports_actioned, reports_dismissed, blobs_removed, users_banned, last_updated)
VALUES (1, 0, 0, 0, 0, 0, 0)
ON CONFLICT (id) DO NOTHING;

-- +migrate Down
DROP TABLE IF EXISTS transparency_stats;
DROP TABLE IF EXISTS blocklist;
