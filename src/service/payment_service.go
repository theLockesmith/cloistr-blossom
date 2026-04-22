package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type paymentService struct {
	config          *config.PaymentConfig
	queries         *db.Queries
	lightningClient core.LightningClient
	cashuClient     core.CashuClient
	log             *zap.Logger
}

// NewPaymentService creates a new payment service.
func NewPaymentService(
	cfg *config.PaymentConfig,
	queries *db.Queries,
	lightningClient core.LightningClient,
	cashuClient core.CashuClient,
	log *zap.Logger,
) (core.PaymentService, error) {
	return &paymentService{
		config:          cfg,
		queries:         queries,
		lightningClient: lightningClient,
		cashuClient:     cashuClient,
		log:             log,
	}, nil
}

// IsEnabled returns true if payment functionality is enabled.
func (s *paymentService) IsEnabled() bool {
	return s.config.Enabled
}

// CalculatePrice calculates the price in satoshis for uploading the given bytes.
func (s *paymentService) CalculatePrice(sizeBytes int64) int64 {
	if sizeBytes <= 0 {
		return 0
	}

	// Calculate price based on satoshis per byte
	price := float64(sizeBytes) * s.config.SatoshisPerByte

	// Round up to nearest satoshi
	priceSats := int64(math.Ceil(price))

	// Apply minimum payment
	if priceSats < s.config.MinPaymentSats {
		priceSats = s.config.MinPaymentSats
	}

	return priceSats
}

// GetFreeTierStatus returns the user's free tier usage status.
func (s *paymentService) GetFreeTierStatus(ctx context.Context, pubkey string) (*core.FreeTierStatus, error) {
	if s.config.FreeBytesLimit == 0 {
		return &core.FreeTierStatus{
			BytesUsed:      0,
			BytesLimit:     0,
			BytesRemaining: 0,
		}, nil
	}

	// Get user's free bytes usage from database
	result, err := s.queries.GetUserFreeBytesUsed(ctx, pubkey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// User doesn't exist yet, full free tier available
			return &core.FreeTierStatus{
				BytesUsed:      0,
				BytesLimit:     s.config.FreeBytesLimit,
				BytesRemaining: s.config.FreeBytesLimit,
			}, nil
		}
		return nil, err
	}

	// Use configured limit or user-specific limit
	limit := s.config.FreeBytesLimit
	if result.FreeBytesLimit > 0 {
		limit = result.FreeBytesLimit
	}

	remaining := limit - result.FreeBytesUsed
	if remaining < 0 {
		remaining = 0
	}

	return &core.FreeTierStatus{
		BytesUsed:      result.FreeBytesUsed,
		BytesLimit:     limit,
		BytesRemaining: remaining,
	}, nil
}

// CanUploadFree returns true if the user can upload the given bytes for free.
func (s *paymentService) CanUploadFree(ctx context.Context, pubkey string, sizeBytes int64) (bool, error) {
	if !s.config.Enabled {
		return true, nil // If payments disabled, everything is free
	}

	if s.config.FreeBytesLimit == 0 {
		return false, nil // No free tier configured
	}

	status, err := s.GetFreeTierStatus(ctx, pubkey)
	if err != nil {
		return false, err
	}

	return status.BytesRemaining >= sizeBytes, nil
}

// CreatePaymentRequest creates a new payment request for an upload.
func (s *paymentService) CreatePaymentRequest(ctx context.Context, pubkey string, sizeBytes int64) (*core.PaymentRequest, error) {
	if !s.config.Enabled {
		return nil, nil // No payment required
	}

	// Check if free tier covers this
	canFree, err := s.CanUploadFree(ctx, pubkey, sizeBytes)
	if err != nil {
		return nil, err
	}
	if canFree {
		return nil, nil // No payment required
	}

	// Calculate price
	amountSats := s.CalculatePrice(sizeBytes)

	// Generate request ID
	requestID := uuid.New().String()

	// Calculate expiry
	now := time.Now()
	expiresAt := now.Add(time.Duration(s.config.RequestExpiryMins) * time.Minute)

	var lightningInvoice, cashuRequest, paymentHash string

	// Create Lightning invoice if enabled
	if s.config.Lightning.Enabled && s.lightningClient != nil && s.lightningClient.IsConnected() {
		memo := s.config.Lightning.InvoiceMemo
		if memo == "" {
			memo = "Blossom Upload"
		}

		invoice, hash, err := s.lightningClient.CreateInvoice(ctx, amountSats, memo)
		if err != nil {
			s.log.Warn("failed to create Lightning invoice", zap.Error(err))
		} else {
			lightningInvoice = invoice
			paymentHash = hash
		}
	}

	// Create Cashu payment request if enabled
	if s.config.Cashu.Enabled && s.cashuClient != nil && s.cashuClient.IsConnected() {
		request, _, err := s.cashuClient.CreatePaymentRequest(ctx, amountSats)
		if err != nil {
			s.log.Warn("failed to create Cashu payment request", zap.Error(err))
		} else {
			cashuRequest = request
		}
	}

	// Must have at least one payment method
	if lightningInvoice == "" && cashuRequest == "" {
		return nil, errors.New("no payment methods available")
	}

	// Store payment request in database
	dbReq, err := s.queries.CreatePaymentRequest(ctx, db.CreatePaymentRequestParams{
		ID:               requestID,
		Pubkey:           pubkey,
		AmountSats:       amountSats,
		BytesRequested:   sizeBytes,
		LightningInvoice: toNullString(lightningInvoice),
		CashuRequest:     toNullString(cashuRequest),
		PaymentHash:      toNullString(paymentHash),
		CreatedAt:        now.Unix(),
		ExpiresAt:        expiresAt.Unix(),
	})
	if err != nil {
		return nil, err
	}

	s.log.Info("payment request created",
		zap.String("id", requestID),
		zap.String("pubkey", pubkey),
		zap.Int64("amount_sats", amountSats),
		zap.Int64("bytes", sizeBytes),
		zap.Bool("has_lightning", lightningInvoice != ""),
		zap.Bool("has_cashu", cashuRequest != ""))

	return &core.PaymentRequest{
		ID:               dbReq.ID,
		Pubkey:           dbReq.Pubkey,
		AmountSats:       dbReq.AmountSats,
		BytesRequested:   dbReq.BytesRequested,
		LightningInvoice: fromNullString(dbReq.LightningInvoice),
		CashuRequest:     fromNullString(dbReq.CashuRequest),
		PaymentHash:      fromNullString(dbReq.PaymentHash),
		Status:           core.PaymentStatus(dbReq.Status),
		CreatedAt:        dbReq.CreatedAt,
		ExpiresAt:        dbReq.ExpiresAt,
	}, nil
}

// GetPaymentRequest retrieves a payment request by ID.
func (s *paymentService) GetPaymentRequest(ctx context.Context, requestID string) (*core.PaymentRequest, error) {
	dbReq, err := s.queries.GetPaymentRequest(ctx, requestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, core.ErrPaymentNotFound
		}
		return nil, err
	}

	return &core.PaymentRequest{
		ID:               dbReq.ID,
		Pubkey:           dbReq.Pubkey,
		AmountSats:       dbReq.AmountSats,
		BytesRequested:   dbReq.BytesRequested,
		LightningInvoice: fromNullString(dbReq.LightningInvoice),
		CashuRequest:     fromNullString(dbReq.CashuRequest),
		PaymentHash:      fromNullString(dbReq.PaymentHash),
		Status:           core.PaymentStatus(dbReq.Status),
		CreatedAt:        dbReq.CreatedAt,
		ExpiresAt:        dbReq.ExpiresAt,
		PaidAt:           fromNullInt64(dbReq.PaidAt),
		ProofData:        fromNullString(dbReq.ProofData),
	}, nil
}

// ValidatePaymentProof validates a payment proof and marks the request as paid.
func (s *paymentService) ValidatePaymentProof(ctx context.Context, proof *core.PaymentProof) error {
	if proof == nil {
		return core.ErrPaymentInvalid
	}

	// Get the payment request
	var paymentReq *core.PaymentRequest
	var err error

	if proof.RequestID != "" {
		paymentReq, err = s.GetPaymentRequest(ctx, proof.RequestID)
	} else if proof.PaymentHash != "" && proof.Method == core.PaymentMethodLightning {
		// Look up by payment hash for Lightning
		dbReq, dbErr := s.queries.GetPaymentRequestByHash(ctx, toNullString(proof.PaymentHash))
		if dbErr != nil {
			if errors.Is(dbErr, sql.ErrNoRows) {
				return core.ErrPaymentNotFound
			}
			return dbErr
		}
		paymentReq = &core.PaymentRequest{
			ID:               dbReq.ID,
			Pubkey:           dbReq.Pubkey,
			AmountSats:       dbReq.AmountSats,
			BytesRequested:   dbReq.BytesRequested,
			LightningInvoice: fromNullString(dbReq.LightningInvoice),
			CashuRequest:     fromNullString(dbReq.CashuRequest),
			PaymentHash:      fromNullString(dbReq.PaymentHash),
			Status:           core.PaymentStatus(dbReq.Status),
			CreatedAt:        dbReq.CreatedAt,
			ExpiresAt:        dbReq.ExpiresAt,
		}
		err = nil
	} else {
		return core.ErrPaymentNotFound
	}

	if err != nil {
		return err
	}

	// Check if already paid
	if paymentReq.Status == core.PaymentStatusPaid {
		return core.ErrPaymentAlreadyPaid
	}

	// Check if expired
	if time.Now().Unix() > paymentReq.ExpiresAt {
		// Mark as expired
		s.queries.MarkPaymentExpired(ctx, paymentReq.ID)
		return core.ErrPaymentExpired
	}

	// Validate based on payment method
	switch proof.Method {
	case core.PaymentMethodLightning:
		if err := s.validateLightningProof(ctx, paymentReq, proof); err != nil {
			return err
		}

	case core.PaymentMethodCashu:
		if err := s.validateCashuProof(ctx, paymentReq, proof); err != nil {
			return err
		}

	default:
		return core.ErrPaymentInvalid
	}

	// Mark as paid
	now := time.Now().Unix()
	err = s.queries.MarkPaymentPaid(ctx, db.MarkPaymentPaidParams{
		PaidAt:    toNullInt64(now),
		ProofData: toNullString(proof.Data),
		ID:        paymentReq.ID,
	})
	if err != nil {
		return err
	}

	s.log.Info("payment verified",
		zap.String("id", paymentReq.ID),
		zap.String("method", string(proof.Method)),
		zap.Int64("amount_sats", paymentReq.AmountSats))

	return nil
}

// validateLightningProof validates a Lightning payment proof.
func (s *paymentService) validateLightningProof(ctx context.Context, req *core.PaymentRequest, proof *core.PaymentProof) error {
	if !s.config.Lightning.Enabled || s.lightningClient == nil {
		return core.ErrLightningDisabled
	}

	// Method 1: Validate preimage directly
	if proof.Data != "" && req.PaymentHash != "" {
		if s.lightningClient.ValidatePreimage(req.PaymentHash, proof.Data) {
			return nil
		}
	}

	// Method 2: Look up invoice status from LND
	if req.PaymentHash != "" {
		paid, _, err := s.lightningClient.LookupInvoice(ctx, req.PaymentHash)
		if err != nil {
			return err
		}
		if paid {
			return nil
		}
	}

	return core.ErrPaymentInvalid
}

// validateCashuProof validates a Cashu payment proof.
func (s *paymentService) validateCashuProof(ctx context.Context, req *core.PaymentRequest, proof *core.PaymentProof) error {
	if !s.config.Cashu.Enabled || s.cashuClient == nil {
		return core.ErrCashuDisabled
	}

	// Verify the token
	amount, err := s.cashuClient.VerifyToken(ctx, proof.Data)
	if err != nil {
		return err
	}

	// Check amount is sufficient
	if amount < req.AmountSats {
		return core.ErrInsufficientPayment
	}

	// Redeem the token
	if err := s.cashuClient.RedeemToken(ctx, proof.Data); err != nil {
		s.log.Warn("failed to redeem Cashu token", zap.Error(err))
		// Don't fail - token was already verified
	}

	return nil
}

// ConsumeFreeTier deducts bytes from the user's free tier allowance.
func (s *paymentService) ConsumeFreeTier(ctx context.Context, pubkey string, sizeBytes int64) error {
	if s.config.FreeBytesLimit == 0 {
		return nil // No free tier to consume
	}

	now := time.Now().Unix()
	return s.queries.IncrementUserFreeBytesUsed(ctx, db.IncrementUserFreeBytesUsedParams{
		FreeBytesUsed: sizeBytes,
		UpdatedAt:     now,
		Pubkey:        pubkey,
	})
}

// GetPaymentStats returns payment statistics for admin dashboard.
func (s *paymentService) GetPaymentStats(ctx context.Context) (*core.PaymentStats, error) {
	stats, err := s.queries.GetPaymentStats(ctx)
	if err != nil {
		return nil, err
	}

	// Convert interface{} values from SUM() which can be NULL
	var totalSatsReceived, totalBytesPaid int64
	if stats.TotalSatsReceived != nil {
		totalSatsReceived = toInt64(stats.TotalSatsReceived)
	}
	if stats.TotalBytesPaid != nil {
		totalBytesPaid = toInt64(stats.TotalBytesPaid)
	}

	return &core.PaymentStats{
		TotalPaid:         stats.TotalPaid,
		TotalPending:      stats.TotalPending,
		TotalExpired:      stats.TotalExpired,
		TotalSatsReceived: totalSatsReceived,
		TotalBytesPaid:    totalBytesPaid,
	}, nil
}

// CleanupExpired marks expired pending payments as expired.
func (s *paymentService) CleanupExpired(ctx context.Context) error {
	now := time.Now().Unix()
	return s.queries.ExpirePendingPayments(ctx, now)
}

// Helper functions for sql.Null types
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func fromNullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func toNullInt64(i int64) sql.NullInt64 {
	return sql.NullInt64{Int64: i, Valid: true}
}

func fromNullInt64(ni sql.NullInt64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return 0
}

// Ensure paymentService implements core.PaymentService
var _ core.PaymentService = (*paymentService)(nil)
