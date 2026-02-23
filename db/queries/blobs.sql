-- name: GetBlobsFromPubkey :many
-- Get all blobs owned by a user via the blob_references table
SELECT b.*
FROM blobs b
INNER JOIN blob_references br ON b.hash = br.hash
WHERE br.pubkey = $1;

-- name: GetBlobsFromPubkeyLegacy :many
-- Legacy query using pubkey column directly (for backward compatibility)
SELECT *
FROM blobs
WHERE pubkey = $1;

-- name: GetBlobFromHash :one
SELECT *
FROM blobs
WHERE hash = $1
LIMIT 1;

-- name: InsertBlob :one
INSERT INTO blobs(
  pubkey,
  hash,
  type,
  size,
  blob,
  created,
  encryption_mode,
  encrypted_dek,
  encryption_nonce,
  original_size,
  ref_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING *;

-- name: DeleteBlobFromHash :exec
DELETE
FROM blobs
WHERE hash = $1;

-- name: IncrementBlobRefCount :exec
UPDATE blobs
SET ref_count = ref_count + 1
WHERE hash = $1;

-- name: DecrementBlobRefCount :one
UPDATE blobs
SET ref_count = ref_count - 1
WHERE hash = $1
RETURNING ref_count;

-- name: GetBlobRefCount :one
SELECT ref_count
FROM blobs
WHERE hash = $1;
