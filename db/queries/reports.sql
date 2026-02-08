-- name: CreateReport :one
INSERT INTO reports (reporter_pubkey, blob_hash, blob_url, reason, details, status, created_at)
VALUES ($1, $2, $3, $4, $5, 'pending', $6)
RETURNING *;

-- name: GetReport :one
SELECT *
FROM reports
WHERE id = $1
LIMIT 1;

-- name: ListPendingReports :many
SELECT *
FROM reports
WHERE status = 'pending'
ORDER BY created_at ASC
LIMIT $1 OFFSET $2;

-- name: ListAllReports :many
SELECT *
FROM reports
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListReportsByStatus :many
SELECT *
FROM reports
WHERE status = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListReportsByBlobHash :many
SELECT *
FROM reports
WHERE blob_hash = $1
ORDER BY created_at DESC;

-- name: UpdateReportStatus :exec
UPDATE reports
SET status = $1, action_taken = $2, reviewed_by = $3, reviewed_at = $4
WHERE id = $5;

-- name: GetPendingReportCount :one
SELECT COUNT(*) as count
FROM reports
WHERE status = 'pending';

-- name: GetReportCountByBlobHash :one
SELECT COUNT(*) as count
FROM reports
WHERE blob_hash = $1;

-- name: GetReportStats :one
SELECT
    COUNT(*) as total_reports,
    COUNT(*) FILTER (WHERE status = 'actioned') as reports_actioned,
    COUNT(*) FILTER (WHERE status = 'dismissed') as reports_dismissed,
    COUNT(*) FILTER (WHERE action_taken = 'removed') as blobs_removed,
    COUNT(*) FILTER (WHERE action_taken = 'user_banned') as users_banned
FROM reports;
