-- name: CreateUploadSession :one
INSERT INTO upload_sessions(
  id,
  pubkey,
  hash,
  total_size,
  chunk_size,
  mime_type,
  chunks_received,
  bytes_received,
  status,
  encryption_mode,
  created,
  updated,
  expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
RETURNING *;

-- name: GetUploadSession :one
SELECT *
FROM upload_sessions
WHERE id = $1
LIMIT 1;

-- name: GetUploadSessionsByPubkey :many
SELECT *
FROM upload_sessions
WHERE pubkey = $1 AND status = 'active'
ORDER BY created DESC;

-- name: UpdateUploadSessionProgress :exec
UPDATE upload_sessions
SET chunks_received = $2,
    bytes_received = $3,
    updated = $4
WHERE id = $1;

-- name: UpdateUploadSessionStatus :exec
UPDATE upload_sessions
SET status = $2,
    updated = $3
WHERE id = $1;

-- name: UpdateUploadSessionHash :exec
UPDATE upload_sessions
SET hash = $2,
    updated = $3
WHERE id = $1;

-- name: DeleteUploadSession :exec
DELETE FROM upload_sessions
WHERE id = $1;

-- name: DeleteExpiredSessions :many
DELETE FROM upload_sessions
WHERE expires_at < $1
RETURNING id;

-- name: CreateUploadChunk :one
INSERT INTO upload_chunks(
  session_id,
  chunk_num,
  size,
  offset_bytes,
  hash,
  received_at
) VALUES ($1,$2,$3,$4,$5,$6)
RETURNING *;

-- name: GetUploadChunks :many
SELECT *
FROM upload_chunks
WHERE session_id = $1
ORDER BY chunk_num;

-- name: GetUploadChunk :one
SELECT *
FROM upload_chunks
WHERE session_id = $1 AND chunk_num = $2
LIMIT 1;

-- name: DeleteUploadChunks :exec
DELETE FROM upload_chunks
WHERE session_id = $1;
