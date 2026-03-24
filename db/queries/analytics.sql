-- Analytics queries for dashboard metrics

-- name: GetUploadsPerDay :many
-- Get upload counts grouped by day within a time range
SELECT
    (created / 86400) * 86400 AS bucket_timestamp,
    COUNT(*) AS upload_count,
    COALESCE(SUM(size), 0) AS total_bytes
FROM blobs
WHERE created >= $1 AND created < $2
GROUP BY bucket_timestamp
ORDER BY bucket_timestamp ASC;

-- name: GetReferencesPerDay :many
-- Get blob reference (user uploads) counts grouped by day
SELECT
    (created / 86400) * 86400 AS bucket_timestamp,
    COUNT(*) AS reference_count
FROM blob_references
WHERE created >= $1 AND created < $2
GROUP BY bucket_timestamp
ORDER BY bucket_timestamp ASC;

-- name: GetNewUsersPerDay :many
-- Get new user registrations grouped by day
SELECT
    (created_at / 86400) * 86400 AS bucket_timestamp,
    COUNT(*) AS user_count
FROM users
WHERE created_at >= $1 AND created_at < $2
GROUP BY bucket_timestamp
ORDER BY bucket_timestamp ASC;

-- name: GetContentTypeBreakdown :many
-- Get blob counts and sizes by MIME type
SELECT
    type AS mime_type,
    COUNT(*) AS blob_count,
    COALESCE(SUM(size), 0) AS total_size
FROM blobs
GROUP BY type
ORDER BY total_size DESC
LIMIT $1;

-- name: GetTopUsersByUsage :many
-- Get users with highest storage usage (using JOIN for efficiency)
SELECT
    u.pubkey,
    u.used_bytes,
    u.updated_at AS last_active,
    COALESCE(br_counts.blob_count, 0) AS blob_count
FROM users u
LEFT JOIN (
    SELECT pubkey, COUNT(*) AS blob_count
    FROM blob_references
    GROUP BY pubkey
) br_counts ON u.pubkey = br_counts.pubkey
WHERE u.used_bytes > 0
ORDER BY u.used_bytes DESC
LIMIT $1;

-- name: GetActiveUsersInPeriod :one
-- Count users with activity (uploads) in a time period
SELECT COUNT(DISTINCT pubkey) AS active_users
FROM blob_references
WHERE created >= $1 AND created < $2;

-- name: GetStorageAtTime :one
-- Get total storage at a specific point in time
SELECT
    COALESCE(SUM(size), 0) AS total_bytes,
    COUNT(*) AS blob_count
FROM blobs
WHERE created < $1;

-- name: GetEncryptionStats :one
-- Get encryption usage statistics
SELECT
    COUNT(*) AS total_blobs,
    COUNT(CASE WHEN encryption_mode = 'server' THEN 1 END) AS server_encrypted,
    COUNT(CASE WHEN encryption_mode = 'e2e' THEN 1 END) AS e2e_encrypted,
    COUNT(CASE WHEN encryption_mode = 'none' OR encryption_mode = '' OR encryption_mode IS NULL THEN 1 END) AS unencrypted
FROM blobs;

-- name: GetDeduplicationStats :one
-- Get deduplication statistics
SELECT
    (SELECT COUNT(*) FROM blobs) AS unique_blobs,
    (SELECT COUNT(*) FROM blob_references) AS total_references,
    (SELECT COALESCE(SUM(size), 0) FROM blobs) AS actual_storage,
    (SELECT COALESCE(SUM(b.size), 0) FROM blob_references br JOIN blobs b ON br.hash = b.hash) AS logical_storage;

-- name: GetRecentActivity :one
-- Get activity counts for the last N seconds
SELECT
    (SELECT COUNT(*) FROM blobs b WHERE b.created >= $1) AS uploads,
    (SELECT COUNT(*) FROM blob_references br WHERE br.created >= $1) AS references,
    (SELECT COALESCE(SUM(b2.size), 0) FROM blobs b2 WHERE b2.created >= $1) AS bytes_uploaded,
    (SELECT COUNT(*) FROM users u WHERE u.created_at >= $1) AS new_users;

-- name: GetUserUsageDistribution :many
-- Get user distribution by usage buckets
-- Returns count of users in each storage tier
SELECT
    CASE
        WHEN used_bytes = 0 THEN 0
        WHEN used_bytes < 1048576 THEN 1048576  -- < 1MB
        WHEN used_bytes < 10485760 THEN 10485760  -- < 10MB
        WHEN used_bytes < 104857600 THEN 104857600  -- < 100MB
        WHEN used_bytes < 1073741824 THEN 1073741824  -- < 1GB
        ELSE 10737418240  -- >= 1GB (10GB bucket)
    END AS max_bytes,
    COUNT(*) AS user_count
FROM users
GROUP BY max_bytes
ORDER BY max_bytes ASC;

-- name: GetStorageGrowthDaily :many
-- Get cumulative storage growth by day (for charts)
-- Uses window function for running total
WITH daily_additions AS (
    SELECT
        (created / 86400) * 86400 AS bucket_timestamp,
        COALESCE(SUM(size), 0) AS day_bytes,
        COUNT(*) AS day_blobs
    FROM blobs
    WHERE created >= $1 AND created < $2
    GROUP BY bucket_timestamp
)
SELECT
    bucket_timestamp,
    day_bytes,
    day_blobs,
    SUM(day_bytes) OVER (ORDER BY bucket_timestamp) AS cumulative_bytes,
    SUM(day_blobs) OVER (ORDER BY bucket_timestamp) AS cumulative_blobs
FROM daily_additions
ORDER BY bucket_timestamp ASC;

-- name: GetCategoryBreakdown :many
-- Get blob counts by content category (derived from MIME type prefix)
SELECT
    CASE
        WHEN type LIKE 'image/%' THEN 'image'
        WHEN type LIKE 'video/%' THEN 'video'
        WHEN type LIKE 'audio/%' THEN 'audio'
        WHEN type LIKE 'text/%' THEN 'text'
        WHEN type LIKE 'application/pdf' THEN 'document'
        WHEN type LIKE 'application/%zip%' OR type LIKE 'application/%tar%' OR type LIKE 'application/%gzip%' THEN 'archive'
        ELSE 'other'
    END AS category,
    COUNT(*) AS blob_count,
    COALESCE(SUM(size), 0) AS total_size
FROM blobs
GROUP BY category
ORDER BY total_size DESC;
