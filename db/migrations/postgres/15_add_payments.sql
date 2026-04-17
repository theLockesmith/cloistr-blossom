-- +migrate Up
-- BUD-07: Payment support for uploads

-- Extend users table for free tier tracking
ALTER TABLE users ADD COLUMN free_bytes_used BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN free_bytes_limit BIGINT NOT NULL DEFAULT 0;

-- Payment requests table for tracking invoices and their status
CREATE TABLE payment_requests (
    id TEXT PRIMARY KEY,
    pubkey TEXT NOT NULL,
    amount_sats BIGINT NOT NULL,
    bytes_requested BIGINT NOT NULL,
    lightning_invoice TEXT,
    cashu_request TEXT,
    payment_hash TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'expired', 'cancelled')),
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    paid_at BIGINT,
    proof_data TEXT
);

-- Indexes for efficient lookups
CREATE INDEX idx_payment_requests_pubkey ON payment_requests(pubkey);
CREATE INDEX idx_payment_requests_status ON payment_requests(status);
CREATE INDEX idx_payment_requests_payment_hash ON payment_requests(payment_hash) WHERE payment_hash IS NOT NULL;
CREATE INDEX idx_payment_requests_expires ON payment_requests(expires_at) WHERE status = 'pending';

-- +migrate Down
DROP INDEX IF EXISTS idx_payment_requests_expires;
DROP INDEX IF EXISTS idx_payment_requests_payment_hash;
DROP INDEX IF EXISTS idx_payment_requests_status;
DROP INDEX IF EXISTS idx_payment_requests_pubkey;
DROP TABLE IF EXISTS payment_requests;
ALTER TABLE users DROP COLUMN IF EXISTS free_bytes_limit;
ALTER TABLE users DROP COLUMN IF EXISTS free_bytes_used;
