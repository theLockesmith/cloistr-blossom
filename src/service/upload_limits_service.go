package service

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
)

// uploadLimitsService implements core.UploadLimitsService.
type uploadLimitsService struct {
	conf           *config.UploadLimitsConfig
	platformClient *platform.Client // nil in standalone mode → TierNamed for all users
	limiter        ratelimit.RateLimiter
	log            *zap.Logger
}

// NewUploadLimitsService constructs the upload limits service.
//
// platformClient may be nil; when nil, every user resolves to TierNamed
// (matching platform.Client.GetTier behaviour in standalone mode).
func NewUploadLimitsService(
	conf *config.UploadLimitsConfig,
	platformClient *platform.Client,
	limiter ratelimit.RateLimiter,
	log *zap.Logger,
) (core.UploadLimitsService, error) {
	return &uploadLimitsService{
		conf:           conf,
		platformClient: platformClient,
		limiter:        limiter,
		log:            log,
	}, nil
}

func (s *uploadLimitsService) IsEnabled() bool {
	return s.conf.Enabled
}

// getTier resolves the tier for pubkey. Falls back to TierAnonymous on error
// (fail closed), and to TierNamed when no platform client is configured.
func (s *uploadLimitsService) getTier(ctx context.Context, pubkey string) platform.Tier {
	if s.platformClient == nil {
		// Standalone mode: all users get Named limits (same as platform.GetTier standalone behaviour).
		return platform.TierNamed
	}
	tier, err := s.platformClient.GetTier(ctx, pubkey)
	if err != nil {
		s.log.Warn("upload_limits: failed to resolve user tier, defaulting to anonymous",
			zap.String("pubkey", pubkey),
			zap.Error(err))
		return platform.TierAnonymous
	}
	return tier
}

// tierConfig returns the per-tier limits for the given tier.
func (s *uploadLimitsService) tierConfig(tier platform.Tier) config.UploadTierLimitsConfig {
	switch tier {
	case platform.TierPaid:
		return s.conf.Paid
	case platform.TierNamed:
		return s.conf.Named
	default: // TierAnonymous and any unknown future tiers
		return s.conf.Anonymous
	}
}

// ValidateMimeType checks the DETECTED (magic-byte) mime type against the
// tier allowlist. An empty AllowedTypePrefixes list means unrestricted.
func (s *uploadLimitsService) ValidateMimeType(ctx context.Context, pubkey string, detectedMime string) error {
	if !s.conf.Enabled {
		return nil
	}
	tier := s.getTier(ctx, pubkey)
	tc := s.tierConfig(tier)
	if len(tc.AllowedTypePrefixes) == 0 {
		return nil // no per-tier allowlist configured
	}
	for _, prefix := range tc.AllowedTypePrefixes {
		if strings.HasPrefix(detectedMime, prefix) {
			return nil
		}
	}
	s.log.Debug("upload_limits: mime type blocked",
		zap.String("pubkey", pubkey),
		zap.String("tier", string(tier)),
		zap.String("mime", detectedMime))
	return core.ErrUploadMimeTypeNotAllowed
}

// ValidateFileSize checks the upload size against the per-tier cap.
// A MaxFileBytes of 0 means no per-tier cap (global limit applies).
func (s *uploadLimitsService) ValidateFileSize(ctx context.Context, pubkey string, sizeBytes int) error {
	if !s.conf.Enabled {
		return nil
	}
	tier := s.getTier(ctx, pubkey)
	tc := s.tierConfig(tier)
	if tc.MaxFileBytes == 0 {
		return nil // no per-tier cap configured
	}
	if int64(sizeBytes) > tc.MaxFileBytes {
		s.log.Debug("upload_limits: file too large",
			zap.String("pubkey", pubkey),
			zap.String("tier", string(tier)),
			zap.Int("size", sizeBytes),
			zap.Int64("max", tc.MaxFileBytes))
		return core.ErrUploadFileTooLarge
	}
	return nil
}

// TrackAndCheckDailyUploads uses a 24-hour sliding-window counter to enforce
// the per-tier UploadsPerDay cap. A value of 0 means no cap.
func (s *uploadLimitsService) TrackAndCheckDailyUploads(ctx context.Context, pubkey string) error {
	if !s.conf.Enabled {
		return nil
	}
	tier := s.getTier(ctx, pubkey)
	tc := s.tierConfig(tier)
	if tc.UploadsPerDay == 0 {
		return nil // no per-day cap configured for this tier
	}
	key := "upload_day:" + pubkey
	allowed, _, _ := s.limiter.Allow(ctx, key, tc.UploadsPerDay, 24*time.Hour)
	if !allowed {
		s.log.Debug("upload_limits: daily upload limit exceeded",
			zap.String("pubkey", pubkey),
			zap.String("tier", string(tier)),
			zap.Int("limit", tc.UploadsPerDay))
		return core.ErrDailyUploadLimitReached
	}
	return nil
}
