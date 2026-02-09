-- +migrate Up
CREATE TABLE IF NOT EXISTS reports
(
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    reporter_pubkey TEXT,                                   -- NULL for anonymous reports
    blob_hash       TEXT NOT NULL,
    blob_url        TEXT NOT NULL,
    reason          TEXT NOT NULL,                          -- 'csam', 'illegal', 'copyright', 'abuse', 'other'
    details         TEXT,                                   -- Additional context from reporter
    status          TEXT NOT NULL DEFAULT 'pending',        -- 'pending', 'reviewed', 'actioned', 'dismissed'
    action_taken    TEXT,                                   -- 'removed', 'user_banned', 'none'
    reviewed_by     TEXT,                                   -- Admin pubkey who reviewed
    created_at      INTEGER NOT NULL,
    reviewed_at     INTEGER
);

CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
CREATE INDEX IF NOT EXISTS idx_reports_blob_hash ON reports(blob_hash);
CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at);

-- +migrate Down
DROP TABLE IF EXISTS reports;
