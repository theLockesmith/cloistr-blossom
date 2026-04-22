package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type expirationService struct {
	queries  *db.Queries
	storage  storage.StorageBackend
	config   core.ExpirationConfig
	log      *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExpirationService creates a new expiration service.
func NewExpirationService(
	queries *db.Queries,
	storageBackend storage.StorageBackend,
	config core.ExpirationConfig,
	log *zap.Logger,
) core.ExpirationService {
	return &expirationService{
		queries: queries,
		storage: storageBackend,
		config:  config,
		log:     log,
		stopCh:  make(chan struct{}),
	}
}

// SetExpiration sets the expiration time for a blob.
func (s *expirationService) SetExpiration(ctx context.Context, hash string, expiresAt time.Time) error {
	return s.queries.SetBlobExpiration(ctx, db.SetBlobExpirationParams{
		Hash:      hash,
		ExpiresAt: sql.NullInt64{Int64: expiresAt.Unix(), Valid: true},
	})
}

// SetExpirationTTL sets the expiration time relative to now.
func (s *expirationService) SetExpirationTTL(ctx context.Context, hash string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	return s.SetExpiration(ctx, hash, expiresAt)
}

// ClearExpiration removes expiration from a blob.
func (s *expirationService) ClearExpiration(ctx context.Context, hash string) error {
	return s.queries.ClearBlobExpiration(ctx, hash)
}

// GetExpiredBlobs returns blobs that have expired.
func (s *expirationService) GetExpiredBlobs(ctx context.Context, limit int) ([]core.ExpiredBlob, error) {
	now := time.Now().Unix()
	if s.config.GracePeriod > 0 {
		now -= int64(s.config.GracePeriod.Seconds())
	}

	rows, err := s.queries.GetExpiredBlobs(ctx, db.GetExpiredBlobsParams{
		ExpiresAt: sql.NullInt64{Int64: now, Valid: true},
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, err
	}

	blobs := make([]core.ExpiredBlob, len(rows))
	for i, row := range rows {
		blobs[i] = core.ExpiredBlob{
			Hash:    row.Hash,
			Pubkey:  row.Pubkey,
			Type:    row.Type,
			Size:    row.Size,
			Created: row.Created,
		}
	}

	return blobs, nil
}

// CleanupExpired deletes expired blobs and returns the count.
func (s *expirationService) CleanupExpired(ctx context.Context) (int, error) {
	now := time.Now().Unix()
	if s.config.GracePeriod > 0 {
		now -= int64(s.config.GracePeriod.Seconds())
	}

	// Delete from database and get hashes
	deletedHashes, err := s.queries.DeleteExpiredBlobs(ctx, sql.NullInt64{Int64: now, Valid: true})
	if err != nil {
		return 0, err
	}

	if len(deletedHashes) == 0 {
		return 0, nil
	}

	// Delete from storage backend
	for _, hash := range deletedHashes {
		if err := s.storage.Delete(ctx, hash); err != nil {
			s.log.Warn("failed to delete expired blob from storage",
				zap.String("hash", hash),
				zap.Error(err))
			// Continue with other deletions
		}
	}

	s.log.Info("expired blobs cleaned up", zap.Int("count", len(deletedHashes)))

	return len(deletedHashes), nil
}

// CountExpired returns the number of expired blobs pending deletion.
func (s *expirationService) CountExpired(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	return s.queries.CountExpiredBlobs(ctx, sql.NullInt64{Int64: now, Valid: true})
}

// ApplyPolicy applies an expiration policy to a blob if it matches.
func (s *expirationService) ApplyPolicy(ctx context.Context, hash string, mimeType string, size int64, pubkey string) (bool, error) {
	// Find matching policy
	policy, err := s.queries.GetMatchingPolicy(ctx, db.GetMatchingPolicyParams{
		Pubkey:     sql.NullString{String: pubkey, Valid: true},
		MimePrefix: sql.NullString{String: mimeType, Valid: true},
		MaxSize:    sql.NullInt64{Int64: size, Valid: true},
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // No matching policy
		}
		return false, err
	}

	// Apply TTL
	ttl := time.Duration(policy.TtlSeconds) * time.Second
	if err := s.SetExpirationTTL(ctx, hash, ttl); err != nil {
		return false, err
	}

	s.log.Debug("expiration policy applied",
		zap.String("hash", hash),
		zap.String("policy", policy.Name),
		zap.Duration("ttl", ttl))

	return true, nil
}

// GetPolicies returns all enabled expiration policies.
func (s *expirationService) GetPolicies(ctx context.Context) ([]core.ExpirationPolicy, error) {
	rows, err := s.queries.GetExpirationPolicies(ctx)
	if err != nil {
		return nil, err
	}

	policies := make([]core.ExpirationPolicy, len(rows))
	for i, row := range rows {
		policies[i] = s.dbPolicyToCore(row)
	}

	return policies, nil
}

// CreatePolicy creates a new expiration policy.
func (s *expirationService) CreatePolicy(ctx context.Context, policy *core.ExpirationPolicy) (*core.ExpirationPolicy, error) {
	now := time.Now().Unix()

	row, err := s.queries.CreateExpirationPolicy(ctx, db.CreateExpirationPolicyParams{
		Name:       policy.Name,
		MimePrefix: sql.NullString{String: policy.MimePrefix, Valid: policy.MimePrefix != ""},
		TtlSeconds: policy.TTLSeconds,
		MaxSize:    sql.NullInt64{Int64: policy.MaxSize, Valid: policy.MaxSize > 0},
		Pubkey:     sql.NullString{String: policy.Pubkey, Valid: policy.Pubkey != ""},
		Priority:   policy.Priority,
		Enabled:    policy.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		return nil, err
	}

	result := s.dbPolicyToCore(row)
	return &result, nil
}

// UpdatePolicy updates an existing policy.
func (s *expirationService) UpdatePolicy(ctx context.Context, policy *core.ExpirationPolicy) error {
	return s.queries.UpdateExpirationPolicy(ctx, db.UpdateExpirationPolicyParams{
		ID:         policy.ID,
		MimePrefix: sql.NullString{String: policy.MimePrefix, Valid: policy.MimePrefix != ""},
		TtlSeconds: policy.TTLSeconds,
		MaxSize:    sql.NullInt64{Int64: policy.MaxSize, Valid: policy.MaxSize > 0},
		Pubkey:     sql.NullString{String: policy.Pubkey, Valid: policy.Pubkey != ""},
		Priority:   policy.Priority,
		Enabled:    policy.Enabled,
		UpdatedAt:  time.Now().Unix(),
	})
}

// DeletePolicy removes a policy.
func (s *expirationService) DeletePolicy(ctx context.Context, id int32) error {
	return s.queries.DeleteExpirationPolicy(ctx, id)
}

// StartCleanupWorker starts the background cleanup worker.
func (s *expirationService) StartCleanupWorker(ctx context.Context) {
	if !s.config.Enabled {
		s.log.Info("expiration cleanup worker disabled")
		return
	}

	interval := s.config.CleanupInterval
	if interval == 0 {
		interval = 1 * time.Hour
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		s.log.Info("expiration cleanup worker started", zap.Duration("interval", interval))

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				s.log.Info("expiration cleanup worker stopped")
				return
			case <-ticker.C:
				if _, err := s.CleanupExpired(ctx); err != nil {
					s.log.Error("cleanup expired blobs failed", zap.Error(err))
				}
			}
		}
	}()
}

// StopCleanupWorker stops the background cleanup worker.
func (s *expirationService) StopCleanupWorker() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// dbPolicyToCore converts a database policy to a core policy.
func (s *expirationService) dbPolicyToCore(row db.ExpirationPolicy) core.ExpirationPolicy {
	policy := core.ExpirationPolicy{
		ID:         row.ID,
		Name:       row.Name,
		TTLSeconds: row.TtlSeconds,
		Priority:   row.Priority,
		Enabled:    row.Enabled,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}

	if row.MimePrefix.Valid {
		policy.MimePrefix = row.MimePrefix.String
	}
	if row.MaxSize.Valid {
		policy.MaxSize = row.MaxSize.Int64
	}
	if row.Pubkey.Valid {
		policy.Pubkey = row.Pubkey.String
	}

	return policy
}

// Ensure interface compliance
var _ core.ExpirationService = (*expirationService)(nil)
