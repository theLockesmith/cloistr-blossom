package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// ErrSweepSkipped is returned by CleanupExpired when another replica holds the
// advisory lock, so this replica did no work. It is not a failure: callers use
// it to distinguish "another replica swept" from "this replica swept and found
// nothing" (e.g. so a liveness timestamp is not advanced on a skipped tick).
var ErrSweepSkipped = errors.New("expiration sweep skipped: lock held by another replica")

// expirationLockKey is the Postgres advisory-lock key the cleanup worker holds
// while sweeping, so only one replica sweeps at a time (there is no k8s leader
// election). The value is arbitrary but must be stable and unique among any
// advisory locks the application uses.
const expirationLockKey int64 = 0x626c6f62_65787000 // "blobexp\0"

type expirationService struct {
	rawDB    *sql.DB
	queries  *db.Queries
	storage  storage.StorageBackend
	quota    core.QuotaService
	config   core.ExpirationConfig
	log      *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExpirationService creates a new expiration service. rawDB is used to hold a
// cross-replica advisory lock during cleanup; quota is used to recalculate
// per-owner usage when an expired blob is deleted (may be nil in tests, in which
// case quota recalculation is skipped).
func NewExpirationService(
	rawDB *sql.DB,
	queries *db.Queries,
	storageBackend storage.StorageBackend,
	quota core.QuotaService,
	config core.ExpirationConfig,
	log *zap.Logger,
) core.ExpirationService {
	return &expirationService{
		rawDB:   rawDB,
		queries: queries,
		storage: storageBackend,
		quota:   quota,
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

// CleanupExpired deletes expired blobs and returns the count. It holds a
// cross-replica advisory lock for the duration so that, with more than one
// replica running the worker, only one sweeps at a time. If the lock is already
// held by another replica the run is skipped (returns 0, nil).
func (s *expirationService) CleanupExpired(ctx context.Context) (int, error) {
	// A dedicated connection is required so the lock is acquired and released on
	// the same session (advisory locks are session-scoped).
	conn, err := s.rawDB.Conn(ctx)
	if err != nil {
		metrics.ExpirationSweepsTotal.WithLabelValues("error").Inc()
		return 0, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", expirationLockKey).Scan(&locked); err != nil {
		metrics.ExpirationSweepsTotal.WithLabelValues("error").Inc()
		return 0, err
	}
	if !locked {
		s.log.Debug("expiration sweep skipped: another replica holds the lock")
		metrics.ExpirationSweepsTotal.WithLabelValues("skipped_locked").Inc()
		return 0, ErrSweepSkipped
	}
	defer func() {
		// Release on the same connection; use a fresh context so shutdown
		// cancellation of ctx does not leave the lock held until the session ends.
		if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", expirationLockKey); err != nil {
			s.log.Warn("failed to release expiration advisory lock", zap.Error(err))
		}
	}()

	deleted, err := s.cleanupExpiredLocked(ctx)
	if err != nil {
		metrics.ExpirationSweepsTotal.WithLabelValues("error").Inc()
		return deleted, err
	}
	metrics.ExpirationSweepsTotal.WithLabelValues("ok").Inc()
	return deleted, nil
}

// cleanupExpiredLocked performs the actual deletion. The caller holds the
// advisory lock.
func (s *expirationService) cleanupExpiredLocked(ctx context.Context) (int, error) {
	// Bound each run to BatchSize so a large backlog of expired blobs is drained
	// across multiple cleanup ticks rather than in one unbounded transaction.
	limit := s.config.BatchSize
	if limit <= 0 {
		limit = 1000
	}

	// GetExpiredBlobs already applies the configured grace period.
	expired, err := s.GetExpiredBlobs(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(expired) == 0 {
		return 0, nil
	}

	deleted := 0
	affectedOwners := make(map[string]struct{})
	for _, blob := range expired {
		// Capture owners BEFORE deletion: the blob_references FK is ON DELETE
		// CASCADE, so once the blobs row is gone the ownership rows vanish and
		// we can no longer tell whose quota to recalculate.
		owners, err := s.queries.GetBlobOwners(ctx, blob.Hash)
		if err != nil {
			// Fall back to the primary uploader pubkey so the common single-owner
			// case still gets its quota recalculated even when the full owner
			// lookup fails. The multi-owner dedup case may still be missed here.
			s.log.Warn("failed to fetch blob owners; falling back to primary pubkey",
				zap.String("hash", blob.Hash),
				zap.Error(err))
			if blob.Pubkey != "" {
				owners = []string{blob.Pubkey}
			}
		}

		// Remove the database record first so an expired blob never re-surfaces
		// even if the storage delete fails (it is retried next run otherwise).
		if err := s.queries.DeleteBlobFromHash(ctx, blob.Hash); err != nil {
			s.log.Warn("failed to delete expired blob record",
				zap.String("hash", blob.Hash),
				zap.Error(err))
			continue
		}

		if err := s.storage.Delete(ctx, blob.Hash); err != nil {
			s.log.Warn("failed to delete expired blob from storage",
				zap.String("hash", blob.Hash),
				zap.Error(err))
			// Continue: the DB record is already gone.
		}

		for _, o := range owners {
			affectedOwners[o] = struct{}{}
		}
		deleted++
	}

	// Recalculate quota usage for every owner that lost a blob. The previous
	// direct-delete path skipped this, leaving used_bytes permanently inflated
	// for anyone whose blobs expired.
	if s.quota != nil {
		for owner := range affectedOwners {
			if err := s.quota.RecalculateUsage(ctx, owner); err != nil {
				s.log.Warn("failed to recalculate quota after expiration",
					zap.String("pubkey", owner),
					zap.Error(err))
			}
		}
	}

	metrics.ExpiredBlobsDeletedTotal.Add(float64(deleted))

	if deleted > 0 {
		s.log.Info("expired blobs cleaned up",
			zap.Int("count", deleted),
			zap.Int("owners_recalculated", len(affectedOwners)))
	}

	return deleted, nil
}

// CountExpired returns the number of expired blobs pending deletion. It applies
// the same grace period as GetExpiredBlobs so the count matches what the next
// sweep will actually delete (otherwise the pending gauge would over-report
// whenever a grace period is configured).
func (s *expirationService) CountExpired(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	if s.config.GracePeriod > 0 {
		now -= int64(s.config.GracePeriod.Seconds())
	}
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
				s.runSweep(ctx)
			}
		}
	}()
}

// runSweep performs one worker tick: it publishes a heartbeat (so the worker's
// liveness is observable even when nothing is expiring — the old code logged
// nothing on empty runs) and then runs the cleanup.
func (s *expirationService) runSweep(ctx context.Context) {
	if pending, err := s.CountExpired(ctx); err != nil {
		s.log.Warn("expiration heartbeat: failed to count expired blobs", zap.Error(err))
	} else {
		metrics.PendingExpiredBlobs.Set(float64(pending))
		s.log.Debug("expiration sweep tick", zap.Int64("pending", pending))
	}

	if _, err := s.CleanupExpired(ctx); err != nil {
		// A lock-skip means another replica swept; it is not a failure and must
		// not advance this replica's last-run timestamp (otherwise a stale
		// timestamp on a replica whose peer has died would be undetectable).
		if errors.Is(err, ErrSweepSkipped) {
			return
		}
		s.log.Error("cleanup expired blobs failed", zap.Error(err))
		return
	}
	metrics.ExpirationLastRunTimestamp.Set(float64(time.Now().Unix()))
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
