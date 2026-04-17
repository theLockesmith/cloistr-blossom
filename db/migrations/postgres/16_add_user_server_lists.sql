-- +migrate Up
-- BUD-03: User Server List storage
-- Stores the server list from each user's kind 10063 event

CREATE TABLE IF NOT EXISTS user_server_lists (
    pubkey      TEXT NOT NULL,
    server_url  TEXT NOT NULL,
    rank        INTEGER NOT NULL DEFAULT 0,  -- Position in list (0 = primary)
    event_id    TEXT NOT NULL,               -- kind 10063 event ID
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL,
    PRIMARY KEY (pubkey, server_url)
);

CREATE INDEX IF NOT EXISTS idx_user_server_lists_pubkey ON user_server_lists(pubkey);
CREATE INDEX IF NOT EXISTS idx_user_server_lists_server ON user_server_lists(server_url);
CREATE INDEX IF NOT EXISTS idx_user_server_lists_rank ON user_server_lists(pubkey, rank);

-- +migrate Down
DROP INDEX IF EXISTS idx_user_server_lists_rank;
DROP INDEX IF EXISTS idx_user_server_lists_server;
DROP INDEX IF EXISTS idx_user_server_lists_pubkey;
DROP TABLE IF EXISTS user_server_lists;
