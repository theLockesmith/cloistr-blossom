package service

import (
	"context"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
)

// platformQuotaService implements QuotaService using the unified platform database.
// In platform mode, quotas are managed centrally in the platform schema.
type platformQuotaService struct {
	client       *platform.Client
	defaultQuota int64
	maxQuota     int64
	enabled      bool
	log          *zap.Logger
}

// NewPlatformQuotaService creates a quota service backed by the unified platform database.
func NewPlatformQuotaService(
	client *platform.Client,
	defaultQuotaBytes int64,
	maxQuotaBytes int64,
	enabled bool,
	log *zap.Logger,
) (core.QuotaService, error) {
	if defaultQuotaBytes == 0 {
		defaultQuotaBytes = 1 * 1024 * 1024 * 1024 // 1 GB default
	}
	if maxQuotaBytes == 0 {
		maxQuotaBytes = 100 * 1024 * 1024 * 1024 // 100 GB max
	}

	return &platformQuotaService{
		client:       client,
		defaultQuota: defaultQuotaBytes,
		maxQuota:     maxQuotaBytes,
		enabled:      enabled,
		log:          log,
	}, nil
}

func (s *platformQuotaService) IsEnabled() bool {
	return s.enabled
}

func (s *platformQuotaService) GetUser(ctx context.Context, pubkey string) (*core.User, error) {
	// In platform mode, we query the platform for quota info
	quotaInfo, err := s.client.GetQuota(ctx, pubkey, platform.QuotaTypeStorageBytes)
	if err != nil {
		if err == platform.ErrAccessDenied {
			return nil, core.ErrUserNotFound
		}
		return nil, err
	}

	// Check if user is banned (access denied means banned or no access)
	hasAccess, _ := s.client.HasAccess(ctx, pubkey)

	return &core.User{
		Pubkey:     pubkey,
		QuotaBytes: quotaInfo.Limit,
		UsedBytes:  quotaInfo.CurrentUsage,
		IsBanned:   !hasAccess,
		CreatedAt:  time.Now().Unix(), // Platform doesn't expose this
		UpdatedAt:  time.Now().Unix(),
	}, nil
}

func (s *platformQuotaService) GetOrCreateUser(ctx context.Context, pubkey string) (*core.User, error) {
	// In platform mode, users are created when they first authenticate
	// We just return their current state or default values
	user, err := s.GetUser(ctx, pubkey)
	if err == core.ErrUserNotFound {
		// Return default user with default quota
		return &core.User{
			Pubkey:     pubkey,
			QuotaBytes: s.defaultQuota,
			UsedBytes:  0,
			IsBanned:   false,
			CreatedAt:  time.Now().Unix(),
			UpdatedAt:  time.Now().Unix(),
		}, nil
	}
	return user, err
}

func (s *platformQuotaService) GetQuotaInfo(ctx context.Context, pubkey string) (*core.QuotaInfo, error) {
	quotaInfo, err := s.client.GetQuota(ctx, pubkey, platform.QuotaTypeStorageBytes)
	if err != nil {
		if err == platform.ErrAccessDenied {
			// User doesn't exist in platform, return default quota
			return &core.QuotaInfo{
				QuotaBytes:     s.defaultQuota,
				UsedBytes:      0,
				AvailableBytes: s.defaultQuota,
				UsagePercent:   0,
			}, nil
		}
		return nil, err
	}

	var usagePercent float64
	if quotaInfo.Limit > 0 {
		usagePercent = float64(quotaInfo.CurrentUsage) / float64(quotaInfo.Limit) * 100
	}

	return &core.QuotaInfo{
		QuotaBytes:     quotaInfo.Limit,
		UsedBytes:      quotaInfo.CurrentUsage,
		AvailableBytes: quotaInfo.Remaining,
		UsagePercent:   usagePercent,
	}, nil
}

func (s *platformQuotaService) CheckQuota(ctx context.Context, pubkey string, additionalBytes int64) error {
	if !s.enabled {
		return nil
	}

	// Check if user has access (not banned)
	if err := s.client.RequireAccess(ctx, pubkey); err != nil {
		return core.ErrUserBanned
	}

	// Check quota
	if err := s.client.RequireQuota(ctx, pubkey, platform.QuotaTypeStorageBytes, additionalBytes); err != nil {
		if err == platform.ErrQuotaExceeded {
			s.log.Warn("quota exceeded",
				zap.String("pubkey", pubkey),
				zap.Int64("requested", additionalBytes))
			return core.ErrQuotaExceeded
		}
		return err
	}

	return nil
}

func (s *platformQuotaService) IncrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	return s.client.RecordUsage(ctx, pubkey, platform.QuotaTypeStorageBytes, bytes)
}

func (s *platformQuotaService) DecrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	return s.client.ReleaseUsage(ctx, pubkey, platform.QuotaTypeStorageBytes, bytes)
}

func (s *platformQuotaService) SetQuota(ctx context.Context, pubkey string, quotaBytes int64) error {
	// Platform mode: quotas are managed centrally via admin UI
	// This operation is not supported in platform mode
	s.log.Warn("SetQuota called in platform mode - quotas are managed centrally",
		zap.String("pubkey", pubkey),
		zap.Int64("requested_quota", quotaBytes))
	return nil
}

func (s *platformQuotaService) BanUser(ctx context.Context, pubkey string) error {
	// Platform mode: bans are managed centrally via admin UI
	s.log.Warn("BanUser called in platform mode - bans are managed centrally",
		zap.String("pubkey", pubkey))
	return nil
}

func (s *platformQuotaService) UnbanUser(ctx context.Context, pubkey string) error {
	// Platform mode: bans are managed centrally via admin UI
	s.log.Warn("UnbanUser called in platform mode - bans are managed centrally",
		zap.String("pubkey", pubkey))
	return nil
}

func (s *platformQuotaService) ListUsers(ctx context.Context, limit, offset int64) ([]*core.User, error) {
	// Platform mode: user listing is done via admin UI
	s.log.Warn("ListUsers called in platform mode - use admin UI")
	return []*core.User{}, nil
}

func (s *platformQuotaService) GetUserCount(ctx context.Context) (int64, error) {
	// Platform mode: user count is available via admin UI
	s.log.Warn("GetUserCount called in platform mode - use admin UI")
	return 0, nil
}

func (s *platformQuotaService) RecalculateUsage(ctx context.Context, pubkey string) error {
	// In platform mode, we'd need to recalculate from blob references
	// This is a complex operation that should be done via admin tools
	s.log.Warn("RecalculateUsage called in platform mode",
		zap.String("pubkey", pubkey))
	return nil
}
