-- name: AddToBlocklist :one
INSERT INTO blocklist (pubkey, reason, blocked_by, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (pubkey) DO UPDATE SET reason = excluded.reason, blocked_by = excluded.blocked_by
RETURNING *;

-- name: RemoveFromBlocklist :exec
DELETE FROM blocklist
WHERE pubkey = $1;

-- name: IsBlocked :one
SELECT EXISTS(
    SELECT 1 FROM blocklist WHERE pubkey = $1
) as is_blocked;

-- name: GetBlocklistEntry :one
SELECT *
FROM blocklist
WHERE pubkey = $1
LIMIT 1;

-- name: ListBlocklist :many
SELECT *
FROM blocklist
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetBlocklistCount :one
SELECT COUNT(*) as count
FROM blocklist;

-- name: GetTransparencyStats :one
SELECT *
FROM transparency_stats
WHERE id = 1;

-- name: UpdateTransparencyStats :exec
UPDATE transparency_stats
SET total_reports = $1,
    reports_actioned = $2,
    reports_dismissed = $3,
    blobs_removed = $4,
    users_banned = $5,
    last_updated = $6
WHERE id = 1;
