-- +migrate Up
CREATE TABLE IF NOT EXISTS blobs
(
    pubkey  TEXT NOT NULL,
    hash    TEXT PRIMARY KEY,
    type    TEXT NOT NULL,
    size    BIGINT NOT NULL,
    blob    BYTEA NOT NULL,
    created BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_blobs_pubkey ON blobs(pubkey);

-- +migrate Down
DROP TABLE IF EXISTS blobs;
