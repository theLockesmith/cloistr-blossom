-- name: CreateTusUpload :exec
INSERT INTO tus_uploads(
  id,
  pubkey,
  size,
  upload_offset,
  metadata,
  created_at,
  expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: GetTusUpload :one
SELECT *
FROM tus_uploads
WHERE id = $1
LIMIT 1;

-- name: AdvanceTusUploadOffset :execrows
UPDATE tus_uploads
SET upload_offset = $3, expires_at = $4
WHERE id = $1 AND upload_offset = $2;

-- name: CreateTusUploadChunk :exec
INSERT INTO tus_upload_chunks(
  upload_id,
  chunk_offset,
  size,
  object_key
) VALUES ($1,$2,$3,$4);

-- name: GetTusUploadChunks :many
SELECT *
FROM tus_upload_chunks
WHERE upload_id = $1
ORDER BY chunk_offset;

-- name: DeleteTusUpload :execrows
DELETE FROM tus_uploads
WHERE id = $1;

-- name: DeleteTusUploadChunks :many
DELETE FROM tus_upload_chunks
WHERE upload_id = $1
RETURNING object_key;

-- name: GetExpiredTusUploadIDs :many
SELECT id
FROM tus_uploads
WHERE expires_at < $1
ORDER BY id
LIMIT $2;

-- name: DeleteTusUploadIfExpired :execrows
DELETE FROM tus_uploads
WHERE id = $1 AND expires_at < $2;
