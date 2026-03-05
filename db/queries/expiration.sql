-- name: SetBlobExpiration :exec
UPDATE blobs
SET expires_at = $2
WHERE hash = $1;

-- name: ClearBlobExpiration :exec
UPDATE blobs
SET expires_at = NULL
WHERE hash = $1;

-- name: GetExpiredBlobs :many
SELECT hash, pubkey, type, size, created
FROM blobs
WHERE expires_at IS NOT NULL
  AND expires_at <= $1
LIMIT $2;

-- name: DeleteExpiredBlobs :many
DELETE FROM blobs
WHERE expires_at IS NOT NULL
  AND expires_at <= $1
RETURNING hash;

-- name: CountExpiredBlobs :one
SELECT COUNT(*) as count
FROM blobs
WHERE expires_at IS NOT NULL
  AND expires_at <= $1;

-- name: GetExpirationPolicies :many
SELECT *
FROM expiration_policies
WHERE enabled = TRUE
ORDER BY priority DESC;

-- name: GetExpirationPolicy :one
SELECT *
FROM expiration_policies
WHERE id = $1;

-- name: CreateExpirationPolicy :one
INSERT INTO expiration_policies (
    name, mime_prefix, ttl_seconds, max_size, pubkey, priority, enabled, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateExpirationPolicy :exec
UPDATE expiration_policies
SET mime_prefix = $2,
    ttl_seconds = $3,
    max_size = $4,
    pubkey = $5,
    priority = $6,
    enabled = $7,
    updated_at = $8
WHERE id = $1;

-- name: DeleteExpirationPolicy :exec
DELETE FROM expiration_policies
WHERE id = $1;

-- name: GetMatchingPolicy :one
SELECT *
FROM expiration_policies
WHERE enabled = TRUE
  AND (pubkey IS NULL OR pubkey = $1)
  AND (mime_prefix IS NULL OR $2 LIKE mime_prefix || '%')
  AND (max_size IS NULL OR $3 <= max_size)
ORDER BY priority DESC
LIMIT 1;
