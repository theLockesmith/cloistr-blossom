-- name: GetUser :one
SELECT *
FROM users
WHERE pubkey = ?
LIMIT 1;

-- name: GetUserQuota :one
SELECT quota_bytes, used_bytes
FROM users
WHERE pubkey = ?
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (pubkey, quota_bytes, used_bytes, is_banned, created_at, updated_at)
VALUES (?, ?, 0, 0, ?, ?)
RETURNING *;

-- name: UpdateUserUsage :exec
UPDATE users
SET used_bytes = ?, updated_at = ?
WHERE pubkey = ?;

-- name: IncrementUserUsage :exec
UPDATE users
SET used_bytes = used_bytes + ?, updated_at = ?
WHERE pubkey = ?;

-- name: DecrementUserUsage :exec
UPDATE users
SET used_bytes = CASE
    WHEN used_bytes >= ? THEN used_bytes - ?
    ELSE 0
END, updated_at = ?
WHERE pubkey = ?;

-- name: UpdateUserQuota :exec
UPDATE users
SET quota_bytes = ?, updated_at = ?
WHERE pubkey = ?;

-- name: BanUser :exec
UPDATE users
SET is_banned = 1, updated_at = ?
WHERE pubkey = ?;

-- name: UnbanUser :exec
UPDATE users
SET is_banned = 0, updated_at = ?
WHERE pubkey = ?;

-- name: ListUsers :many
SELECT *
FROM users
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: GetUserCount :one
SELECT COUNT(*) as count
FROM users;

-- name: GetOrCreateUser :one
INSERT INTO users (pubkey, quota_bytes, used_bytes, is_banned, created_at, updated_at)
VALUES (?, ?, 0, 0, ?, ?)
ON CONFLICT(pubkey) DO UPDATE SET updated_at = excluded.updated_at
RETURNING *;

-- name: RecalculateUserUsage :exec
UPDATE users
SET used_bytes = (
    SELECT COALESCE(SUM(size), 0)
    FROM blobs
    WHERE blobs.pubkey = users.pubkey
), updated_at = ?
WHERE users.pubkey = ?;
