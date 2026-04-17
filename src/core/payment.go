package core

import (
	"context"
	"errors"
)

// Payment-related errors.
var (
	ErrPaymentRequired     = errors.New("payment required")
	ErrPaymentInvalid      = errors.New("invalid payment proof")
	ErrPaymentExpired      = errors.New("payment request expired")
	ErrPaymentNotFound     = errors.New("payment request not found")
	ErrPaymentAlreadyPaid  = errors.New("payment already processed")
	ErrPaymentDisabled     = errors.New("payments not enabled")
	ErrLightningDisabled   = errors.New("lightning payments not enabled")
	ErrCashuDisabled       = errors.New("cashu payments not enabled")
	ErrInsufficientPayment = errors.New("payment amount insufficient")
)

// PaymentMethod represents the payment method used.
type PaymentMethod string

const (
	PaymentMethodLightning PaymentMethod = "lightning"
	PaymentMethodCashu     PaymentMethod = "cashu"
)

// PaymentStatus represents the status of a payment request.
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusPaid      PaymentStatus = "paid"
	PaymentStatusExpired   PaymentStatus = "expired"
	PaymentStatusCancelled PaymentStatus = "cancelled"
)

// PaymentRequest represents a request for payment.
type PaymentRequest struct {
	ID               string
	Pubkey           string
	AmountSats       int64
	BytesRequested   int64
	LightningInvoice string // BOLT-11 encoded invoice
	CashuRequest     string // NUT-24 payment request
	PaymentHash      string // For Lightning lookup/verification
	Status           PaymentStatus
	CreatedAt        int64
	ExpiresAt        int64
	PaidAt           int64
	ProofData        string
}

// PaymentProof represents a proof of payment from the client.
type PaymentProof struct {
	Method       PaymentMethod
	Data         string // Preimage for Lightning, token for Cashu
	PaymentHash  string // Optional: For Lightning, the payment hash being paid
	RequestID    string // Optional: The payment request ID
}

// PaymentStats contains payment statistics for admin dashboard.
type PaymentStats struct {
	TotalPaid        int64
	TotalPending     int64
	TotalExpired     int64
	TotalSatsReceived int64
	TotalBytesPaid   int64
}

// FreeTierStatus represents a user's free tier usage.
type FreeTierStatus struct {
	BytesUsed    int64
	BytesLimit   int64
	BytesRemaining int64
}

// PaymentService provides payment-related functionality for BUD-07.
type PaymentService interface {
	// IsEnabled returns true if payment functionality is enabled.
	IsEnabled() bool

	// CalculatePrice calculates the price in satoshis for uploading the given bytes.
	CalculatePrice(sizeBytes int64) int64

	// GetFreeTierStatus returns the user's free tier usage status.
	GetFreeTierStatus(ctx context.Context, pubkey string) (*FreeTierStatus, error)

	// CanUploadFree returns true if the user can upload the given bytes for free.
	CanUploadFree(ctx context.Context, pubkey string, sizeBytes int64) (bool, error)

	// CreatePaymentRequest creates a new payment request for an upload.
	// Returns nil if no payment is required (e.g., within free tier).
	CreatePaymentRequest(ctx context.Context, pubkey string, sizeBytes int64) (*PaymentRequest, error)

	// GetPaymentRequest retrieves a payment request by ID.
	GetPaymentRequest(ctx context.Context, requestID string) (*PaymentRequest, error)

	// ValidatePaymentProof validates a payment proof and marks the request as paid.
	ValidatePaymentProof(ctx context.Context, proof *PaymentProof) error

	// ConsumeFreeTier deducts bytes from the user's free tier allowance.
	ConsumeFreeTier(ctx context.Context, pubkey string, sizeBytes int64) error

	// GetPaymentStats returns payment statistics for admin dashboard.
	GetPaymentStats(ctx context.Context) (*PaymentStats, error)

	// CleanupExpired marks expired pending payments as expired.
	CleanupExpired(ctx context.Context) error
}

// LightningClient provides Lightning Network payment functionality.
type LightningClient interface {
	// IsConnected returns true if the Lightning client is connected.
	IsConnected() bool

	// CreateInvoice creates a new Lightning invoice.
	CreateInvoice(ctx context.Context, amountSats int64, memo string) (invoice string, paymentHash string, err error)

	// LookupInvoice checks if an invoice has been paid.
	LookupInvoice(ctx context.Context, paymentHash string) (paid bool, preimage string, err error)

	// ValidatePreimage validates a preimage against a payment hash.
	ValidatePreimage(paymentHash, preimage string) bool
}

// CashuClient provides Cashu ecash payment functionality.
type CashuClient interface {
	// IsConnected returns true if the Cashu client is connected to a mint.
	IsConnected() bool

	// CreatePaymentRequest creates a Cashu payment request (NUT-24 format).
	CreatePaymentRequest(ctx context.Context, amountSats int64) (request string, id string, err error)

	// VerifyToken verifies a Cashu token and returns the amount if valid.
	VerifyToken(ctx context.Context, token string) (amountSats int64, err error)

	// RedeemToken redeems a Cashu token (swaps for new tokens or settles).
	RedeemToken(ctx context.Context, token string) error
}
