-- +migrate Up
CREATE TABLE IF NOT EXISTS users
(
    pubkey       TEXT PRIMARY KEY,
    quota_bytes  BIGINT NOT NULL DEFAULT 1073741824,  -- 1 GB default
    used_bytes   BIGINT NOT NULL DEFAULT 0,
    is_banned    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users(is_banned);

-- +migrate Down
DROP TABLE IF EXISTS users;
