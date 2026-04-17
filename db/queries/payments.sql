-- Payment queries for BUD-07 support

-- name: CreatePaymentRequest :one
INSERT INTO payment_requests (
    id, pubkey, amount_sats, bytes_requested,
    lightning_invoice, cashu_request, payment_hash,
    status, created_at, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
RETURNING *;

-- name: GetPaymentRequest :one
SELECT *
FROM payment_requests
WHERE id = $1
LIMIT 1;

-- name: GetPaymentRequestByHash :one
SELECT *
FROM payment_requests
WHERE payment_hash = $1
LIMIT 1;

-- name: GetPendingPaymentRequests :many
SELECT *
FROM payment_requests
WHERE pubkey = $1 AND status = 'pending' AND expires_at > $2
ORDER BY created_at DESC;

-- name: MarkPaymentPaid :exec
UPDATE payment_requests
SET status = 'paid', paid_at = $1, proof_data = $2
WHERE id = $3;

-- name: MarkPaymentExpired :exec
UPDATE payment_requests
SET status = 'expired'
WHERE id = $1;

-- name: ExpirePendingPayments :exec
-- Batch expire all pending payments past their expiry time
UPDATE payment_requests
SET status = 'expired'
WHERE status = 'pending' AND expires_at < $1;

-- name: GetUserFreeBytesUsed :one
SELECT free_bytes_used, free_bytes_limit
FROM users
WHERE pubkey = $1
LIMIT 1;

-- name: UpdateUserFreeBytesUsed :exec
UPDATE users
SET free_bytes_used = $1, updated_at = $2
WHERE pubkey = $3;

-- name: IncrementUserFreeBytesUsed :exec
UPDATE users
SET free_bytes_used = free_bytes_used + $1, updated_at = $2
WHERE pubkey = $3;

-- name: SetUserFreeBytesLimit :exec
UPDATE users
SET free_bytes_limit = $1, updated_at = $2
WHERE pubkey = $3;

-- name: GetPaymentStats :one
-- Get payment statistics for admin dashboard
SELECT
    COUNT(*) FILTER (WHERE status = 'paid') AS total_paid,
    COUNT(*) FILTER (WHERE status = 'pending') AS total_pending,
    COUNT(*) FILTER (WHERE status = 'expired') AS total_expired,
    COALESCE(SUM(amount_sats) FILTER (WHERE status = 'paid'), 0) AS total_sats_received,
    COALESCE(SUM(bytes_requested) FILTER (WHERE status = 'paid'), 0) AS total_bytes_paid
FROM payment_requests;

-- name: GetRecentPayments :many
-- Get recent payments for admin view
SELECT *
FROM payment_requests
WHERE status = 'paid'
ORDER BY paid_at DESC
LIMIT $1;
