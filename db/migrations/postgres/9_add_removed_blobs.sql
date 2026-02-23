-- +migrate Up
-- Track removed blob hashes to prevent re-upload (BUD-09 compliance)
CREATE TABLE IF NOT EXISTS removed_blobs (
    hash        TEXT PRIMARY KEY,
    reason      TEXT NOT NULL,           -- csam, illegal, copyright, abuse, other
    removed_by  TEXT NOT NULL,           -- Admin pubkey who removed it
    report_id   INTEGER,                 -- Optional: linked report ID
    created_at  BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_removed_blobs_reason ON removed_blobs(reason);
CREATE INDEX IF NOT EXISTS idx_removed_blobs_created_at ON removed_blobs(created_at);

-- +migrate Down
DROP TABLE IF EXISTS removed_blobs;
