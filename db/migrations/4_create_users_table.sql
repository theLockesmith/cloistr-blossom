-- +migrate Up
CREATE TABLE IF NOT EXISTS users
(
    pubkey       TEXT PRIMARY KEY,
    quota_bytes  INTEGER NOT NULL DEFAULT 1073741824,  -- 1 GB default
    used_bytes   INTEGER NOT NULL DEFAULT 0,
    is_banned    INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- +migrate Down
DROP TABLE IF EXISTS users;
