package service

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

type quotaService struct {
	queries      *db.Queries
	config       *config.QuotaConfig
	defaultQuota int64
	log          *zap.Logger
}

func NewQuotaService(
	queries *db.Queries,
	cfg *config.QuotaConfig,
	log *zap.Logger,
) (core.QuotaService, error) {
	defaultQuota := cfg.DefaultBytes
	if defaultQuota == 0 {
		defaultQuota = 1 * 1024 * 1024 * 1024 // 1 GB
	}

	return &quotaService{
		queries:      queries,
		config:       cfg,
		defaultQuota: defaultQuota,
		log:          log,
	}, nil
}

func (s *quotaService) IsEnabled() bool {
	return s.config.Enabled
}

func (s *quotaService) GetUser(ctx context.Context, pubkey string) (*core.User, error) {
	dbUser, err := s.queries.GetUser(ctx, pubkey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrUserNotFound
		}
		return nil, err
	}
	return s.dbUserToCoreUser(dbUser), nil
}

func (s *quotaService) GetOrCreateUser(ctx context.Context, pubkey string) (*core.User, error) {
	now := time.Now().Unix()
	dbUser, err := s.queries.GetOrCreateUser(ctx, db.GetOrCreateUserParams{
		Pubkey:     pubkey,
		QuotaBytes: s.defaultQuota,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		return nil, err
	}
	return s.dbUserToCoreUser(dbUser), nil
}

func (s *quotaService) GetQuotaInfo(ctx context.Context, pubkey string) (*core.QuotaInfo, error) {
	quota, err := s.queries.GetUserQuota(ctx, pubkey)
	if err != nil {
		if err == sql.ErrNoRows {
			// User doesn't exist, return default quota info
			return &core.QuotaInfo{
				QuotaBytes:     s.defaultQuota,
				UsedBytes:      0,
				AvailableBytes: s.defaultQuota,
				UsagePercent:   0,
			}, nil
		}
		return nil, err
	}

	available := quota.QuotaBytes - quota.UsedBytes
	if available < 0 {
		available = 0
	}

	var usagePercent float64
	if quota.QuotaBytes > 0 {
		usagePercent = float64(quota.UsedBytes) / float64(quota.QuotaBytes) * 100
	}

	return &core.QuotaInfo{
		QuotaBytes:     quota.QuotaBytes,
		UsedBytes:      quota.UsedBytes,
		AvailableBytes: available,
		UsagePercent:   usagePercent,
	}, nil
}

func (s *quotaService) CheckQuota(ctx context.Context, pubkey string, additionalBytes int64) error {
	// If quotas are disabled, always allow
	if !s.config.Enabled {
		return nil
	}

	user, err := s.GetOrCreateUser(ctx, pubkey)
	if err != nil {
		return err
	}

	// Check if user is banned
	if user.IsBanned {
		return core.ErrUserBanned
	}

	// Check if upload would exceed quota
	if user.UsedBytes+additionalBytes > user.QuotaBytes {
		s.log.Warn("quota exceeded",
			zap.String("pubkey", pubkey),
			zap.Int64("used", user.UsedBytes),
			zap.Int64("quota", user.QuotaBytes),
			zap.Int64("requested", additionalBytes))
		return core.ErrQuotaExceeded
	}

	return nil
}

func (s *quotaService) IncrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	// Ensure user exists first
	_, err := s.GetOrCreateUser(ctx, pubkey)
	if err != nil {
		return err
	}

	return s.queries.IncrementUserUsage(ctx, db.IncrementUserUsageParams{
		UsedBytes: bytes,
		UpdatedAt: time.Now().Unix(),
		Pubkey:    pubkey,
	})
}

func (s *quotaService) DecrementUsage(ctx context.Context, pubkey string, bytes int64) error {
	return s.queries.DecrementUserUsage(ctx, db.DecrementUserUsageParams{
		UsedBytes:   bytes,
		UsedBytes_2: bytes,
		UpdatedAt:   time.Now().Unix(),
		Pubkey:      pubkey,
	})
}

func (s *quotaService) SetQuota(ctx context.Context, pubkey string, quotaBytes int64) error {
	// Validate quota doesn't exceed max
	if s.config.MaxBytes > 0 && quotaBytes > s.config.MaxBytes {
		quotaBytes = s.config.MaxBytes
	}

	// Ensure user exists
	_, err := s.GetOrCreateUser(ctx, pubkey)
	if err != nil {
		return err
	}

	return s.queries.UpdateUserQuota(ctx, db.UpdateUserQuotaParams{
		QuotaBytes: quotaBytes,
		UpdatedAt:  time.Now().Unix(),
		Pubkey:     pubkey,
	})
}

func (s *quotaService) BanUser(ctx context.Context, pubkey string) error {
	// Ensure user exists
	_, err := s.GetOrCreateUser(ctx, pubkey)
	if err != nil {
		return err
	}

	return s.queries.BanUser(ctx, db.BanUserParams{
		UpdatedAt: time.Now().Unix(),
		Pubkey:    pubkey,
	})
}

func (s *quotaService) UnbanUser(ctx context.Context, pubkey string) error {
	return s.queries.UnbanUser(ctx, db.UnbanUserParams{
		UpdatedAt: time.Now().Unix(),
		Pubkey:    pubkey,
	})
}

func (s *quotaService) ListUsers(ctx context.Context, limit, offset int64) ([]*core.User, error) {
	dbUsers, err := s.queries.ListUsers(ctx, db.ListUsersParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}

	users := make([]*core.User, len(dbUsers))
	for i, dbUser := range dbUsers {
		users[i] = s.dbUserToCoreUser(dbUser)
	}
	return users, nil
}

func (s *quotaService) GetUserCount(ctx context.Context) (int64, error) {
	return s.queries.GetUserCount(ctx)
}

func (s *quotaService) RecalculateUsage(ctx context.Context, pubkey string) error {
	return s.queries.RecalculateUserUsage(ctx, db.RecalculateUserUsageParams{
		UpdatedAt: time.Now().Unix(),
		Pubkey:    pubkey,
	})
}

func (s *quotaService) dbUserToCoreUser(dbUser db.User) *core.User {
	return &core.User{
		Pubkey:     dbUser.Pubkey,
		QuotaBytes: dbUser.QuotaBytes,
		UsedBytes:  dbUser.UsedBytes,
		IsBanned:   dbUser.IsBanned,
		CreatedAt:  dbUser.CreatedAt,
		UpdatedAt:  dbUser.UpdatedAt,
	}
}
