-- name: CreateBlobReference :one
INSERT INTO blob_references (pubkey, hash, created)
VALUES ($1, $2, $3)
ON CONFLICT (pubkey, hash) DO NOTHING
RETURNING *;

-- name: GetBlobReference :one
SELECT *
FROM blob_references
WHERE pubkey = $1 AND hash = $2
LIMIT 1;

-- name: DeleteBlobReference :exec
DELETE FROM blob_references
WHERE pubkey = $1 AND hash = $2;

-- name: GetBlobReferencesByHash :many
SELECT *
FROM blob_references
WHERE hash = $1;

-- name: GetBlobReferencesByPubkey :many
SELECT *
FROM blob_references
WHERE pubkey = $1;

-- name: CountBlobReferences :one
SELECT COUNT(*) as count
FROM blob_references
WHERE hash = $1;

-- name: HasBlobReference :one
SELECT EXISTS(
    SELECT 1 FROM blob_references
    WHERE pubkey = $1 AND hash = $2
) as exists;
