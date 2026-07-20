package core

import (
	"context"
	"errors"
)

var (
	// ErrUploadMimeTypeNotAllowed is returned when the detected MIME type is not
	// permitted for the user's tier. Detection is magic-byte based, never from
	// client-supplied Content-Type.
	ErrUploadMimeTypeNotAllowed = errors.New("mime type not allowed for your account tier")

	// ErrUploadFileTooLarge is returned when the upload exceeds the per-tier size cap.
	ErrUploadFileTooLarge = errors.New("file exceeds the size limit for your account tier")

	// ErrDailyUploadLimitReached is returned when the user has exceeded their
	// per-day upload count for their tier.
	ErrDailyUploadLimitReached = errors.New("daily upload limit reached for your account tier")
)

// UploadLimitsService enforces tier-aware upload restrictions.
//
// All Validate* methods are no-ops when IsEnabled returns false, preserving
// identical behaviour to the pre-enforcement baseline for self-hosters.
//
// Tier resolution uses platform.Client.GetTier in platform mode.
// In standalone mode every user is treated as TierNamed.
type UploadLimitsService interface {
	// IsEnabled reports whether upload_limits.enabled is set in config.
	IsEnabled() bool

	// ValidateMimeType checks the DETECTED mime type (magic-byte based) against
	// the allowlist for the user's tier.
	ValidateMimeType(ctx context.Context, pubkey string, detectedMime string) error

	// ValidateFileSize checks the upload byte count against the per-tier cap.
	ValidateFileSize(ctx context.Context, pubkey string, sizeBytes int) error

	// TrackAndCheckDailyUploads atomically increments the per-pubkey daily
	// upload counter and returns ErrDailyUploadLimitReached if the tier cap
	// is exceeded. The counter window is a 24-hour sliding window.
	// Returns nil when UploadsPerDay is 0 (no cap configured for this tier).
	TrackAndCheckDailyUploads(ctx context.Context, pubkey string) error
}
