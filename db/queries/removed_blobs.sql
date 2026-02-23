-- name: AddRemovedBlob :exec
INSERT INTO removed_blobs (hash, reason, removed_by, report_id, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (hash) DO UPDATE SET
    reason = excluded.reason,
    removed_by = excluded.removed_by,
    report_id = excluded.report_id,
    created_at = excluded.created_at;

-- name: IsHashRemoved :one
SELECT EXISTS(
    SELECT 1 FROM removed_blobs WHERE hash = $1
) as removed;

-- name: GetRemovedBlob :one
SELECT *
FROM removed_blobs
WHERE hash = $1;

-- name: ListRemovedBlobs :many
SELECT *
FROM removed_blobs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetRemovedBlobCount :one
SELECT COUNT(*) as count
FROM removed_blobs;

-- name: DeleteRemovedBlob :exec
DELETE FROM removed_blobs
WHERE hash = $1;
