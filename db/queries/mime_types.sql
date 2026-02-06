-- name: GetMimeType :one
SELECT *
FROM mime_types
WHERE mime_type = $1
LIMIT 1;

-- name: GetAllMimeTypes :many
SELECT *
FROM mime_types;
