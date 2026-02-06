-- name: GetUser :one
SELECT *
FROM users
WHERE pubkey = $1
LIMIT 1;

-- name: GetUserQuota :one
SELECT quota_bytes, used_bytes
FROM users
WHERE pubkey = $1
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (pubkey, quota_bytes, used_bytes, is_banned, created_at, updated_at)
VALUES ($1, $2, 0, 0, $3, $4)
RETURNING *;

-- name: UpdateUserUsage :exec
UPDATE users
SET used_bytes = $1, updated_at = $2
WHERE pubkey = $3;

-- name: IncrementUserUsage :exec
UPDATE users
SET used_bytes = used_bytes + $1, updated_at = $2
WHERE pubkey = $3;

-- name: DecrementUserUsage :exec
UPDATE users
SET used_bytes = CASE
    WHEN used_bytes >= $1 THEN used_bytes - $2
    ELSE 0
END, updated_at = $3
WHERE pubkey = $4;

-- name: UpdateUserQuota :exec
UPDATE users
SET quota_bytes = $1, updated_at = $2
WHERE pubkey = $3;

-- name: BanUser :exec
UPDATE users
SET is_banned = 1, updated_at = $1
WHERE pubkey = $2;

-- name: UnbanUser :exec
UPDATE users
SET is_banned = 0, updated_at = $1
WHERE pubkey = $2;

-- name: ListUsers :many
SELECT *
FROM users
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetUserCount :one
SELECT COUNT(*) as count
FROM users;

-- name: GetOrCreateUser :one
INSERT INTO users (pubkey, quota_bytes, used_bytes, is_banned, created_at, updated_at)
VALUES ($1, $2, 0, 0, $3, $4)
ON CONFLICT(pubkey) DO UPDATE SET updated_at = excluded.updated_at
RETURNING *;

-- name: RecalculateUserUsage :exec
UPDATE users
SET used_bytes = (
    SELECT COALESCE(SUM(size), 0)
    FROM blobs
    WHERE blobs.pubkey = users.pubkey
), updated_at = $1
WHERE users.pubkey = $2;
