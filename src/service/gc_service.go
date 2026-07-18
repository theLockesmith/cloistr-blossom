package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// gcReconcileMaxLimit bounds a single reconcile run so a large backlog is
// drained across multiple invocations rather than in one unbounded pass.
const gcReconcileMaxLimit = 5000

// gcLockKey is the Postgres advisory-lock key the reconcile worker holds while
// deleting, so at most one sweep runs across all replicas and operator calls
// (there is no k8s leader election). It must be stable and distinct from every
// other advisory-lock key the application uses (cf. expirationLockKey).
const gcLockKey int64 = 0x626c6f62_5f67635f // "blob_gc_"

type gcService struct {
	rawDB    *sql.DB
	queries  *db.Queries
	storage  storage.StorageBackend
	config   core.GCConfig
	log      *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewGCService creates a reconciliation service. rawDB is used to hold a
// cross-replica advisory lock during deletion (advisory locks are
// session-scoped, so lock and unlock must run on the same *sql.Conn). storage
// removes the physical object after the guarded DB delete succeeds.
func NewGCService(rawDB *sql.DB, queries *db.Queries, storageBackend storage.StorageBackend, config core.GCConfig, log *zap.Logger) core.GCService {
	return &gcService{
		rawDB:   rawDB,
		queries: queries,
		storage: storageBackend,
		config:  config,
		log:     log,
		stopCh:  make(chan struct{}),
	}
}

// Report returns current reference-bookkeeping drift counts and publishes them
// as gauges. Read-only.
func (s *gcService) Report(ctx context.Context) (*core.GCReport, error) {
	zeroRef, err := s.queries.CountZeroRefBlobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("count zero-ref blobs: %w", err)
	}
	ownerless, err := s.queries.CountOwnerlessBlobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("count ownerless blobs: %w", err)
	}

	metrics.OrphanedBlobs.WithLabelValues("zero_ref").Set(float64(zeroRef))
	metrics.OrphanedBlobs.WithLabelValues("ownerless").Set(float64(ownerless))

	return &core.GCReport{
		ZeroRefBlobs:   zeroRef,
		OwnerlessBlobs: ownerless,
	}, nil
}

// clampLimit normalizes a caller-supplied reconcile limit into the allowed
// range.
func clampGCLimit(limit int) int {
	if limit <= 0 {
		return 1000
	}
	if limit > gcReconcileMaxLimit {
		return gcReconcileMaxLimit
	}
	return limit
}

// Reconcile deletes ownerless blobs (or, in dry-run mode, reports what would be
// deleted). Dry-run is read-only and takes no lock. A wet run serializes on the
// cross-replica advisory lock so the worker, other replicas, and operator calls
// never delete concurrently.
func (s *gcService) Reconcile(ctx context.Context, dryRun bool, limit int) (*core.GCReconcileResult, error) {
	limit = clampGCLimit(limit)

	if dryRun {
		rows, err := s.queries.ListOwnerlessBlobs(ctx, int32(limit))
		if err != nil {
			return nil, fmt.Errorf("list ownerless blobs: %w", err)
		}
		result := &core.GCReconcileResult{
			DryRun:         true,
			OwnerlessFound: int64(len(rows)),
			Hashes:         make([]string, 0, len(rows)),
		}
		for _, r := range rows {
			result.Hashes = append(result.Hashes, r.Hash)
		}
		s.log.Info("gc reconcile dry-run", zap.Int64("ownerless_found", result.OwnerlessFound))
		return result, nil
	}

	return s.reconcileLocked(ctx, limit)
}

// reconcileLocked holds the advisory lock for the duration of a wet reconcile.
// If another sweep already holds the lock the run is skipped (LockSkipped=true,
// nil error). The lock and unlock run on the same dedicated connection because
// Postgres advisory locks are session-scoped.
func (s *gcService) reconcileLocked(ctx context.Context, limit int) (*core.GCReconcileResult, error) {
	conn, err := s.rawDB.Conn(ctx)
	if err != nil {
		metrics.GCSweepsTotal.WithLabelValues("error").Inc()
		return nil, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", gcLockKey).Scan(&locked); err != nil {
		metrics.GCSweepsTotal.WithLabelValues("error").Inc()
		return nil, err
	}
	if !locked {
		metrics.GCSweepsTotal.WithLabelValues("skipped_locked").Inc()
		s.log.Debug("gc reconcile skipped: another sweep holds the lock")
		return &core.GCReconcileResult{LockSkipped: true}, nil
	}
	defer func() {
		// Release on the same connection; use a fresh context so shutdown
		// cancellation of ctx does not leave the lock held until the session ends.
		if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", gcLockKey); err != nil {
			s.log.Warn("failed to release gc advisory lock", zap.Error(err))
		}
	}()

	result, err := s.deleteOwnerless(ctx, limit)
	if err != nil {
		metrics.GCSweepsTotal.WithLabelValues("error").Inc()
		return result, err
	}
	metrics.GCSweepsTotal.WithLabelValues("ok").Inc()
	return result, nil
}

// deleteOwnerless performs the guarded deletion of ownerless blobs. The caller
// holds the advisory lock.
func (s *gcService) deleteOwnerless(ctx context.Context, limit int) (*core.GCReconcileResult, error) {
	rows, err := s.queries.ListOwnerlessBlobs(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list ownerless blobs: %w", err)
	}

	result := &core.GCReconcileResult{
		OwnerlessFound: int64(len(rows)),
		Hashes:         make([]string, 0, len(rows)),
	}

	for _, r := range rows {
		// Guarded delete: the row is removed only if it is STILL ownerless at
		// delete time. If a new owner re-uploaded matching content between the
		// list snapshot and now, DeleteOwnerlessBlob deletes nothing and returns
		// sql.ErrNoRows — we skip it rather than destroy a live blob.
		if _, err := s.queries.DeleteOwnerlessBlob(ctx, r.Hash); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				s.log.Debug("gc: blob gained an owner before delete, skipping",
					zap.String("hash", r.Hash))
				continue
			}
			s.log.Warn("gc: failed to delete ownerless blob record",
				zap.String("hash", r.Hash),
				zap.Error(err))
			continue
		}

		// DB row is gone; remove the physical object. A storage failure is logged
		// but not retried here (the row is already deleted); an orphaned storage
		// object is what the future S3-anti-join sweep is meant to catch.
		if s.storage != nil {
			if err := s.storage.Delete(ctx, r.Hash); err != nil {
				s.log.Warn("gc: failed to delete ownerless blob from storage",
					zap.String("hash", r.Hash),
					zap.Error(err))
			}
		}

		result.Hashes = append(result.Hashes, r.Hash)
		result.Deleted++
	}

	if result.Deleted > 0 {
		metrics.GCReconciledTotal.Add(float64(result.Deleted))
		s.log.Info("gc reconcile deleted ownerless blobs",
			zap.Int64("deleted", result.Deleted),
			zap.Int64("found", result.OwnerlessFound))
	}

	// Refresh the drift gauges to reflect the post-delete state.
	if _, err := s.Report(ctx); err != nil {
		s.log.Warn("gc: failed to refresh report after reconcile", zap.Error(err))
	}

	return result, nil
}

// StartWorker starts the background reconcile worker. It is a no-op when the GC
// worker is disabled in config.
func (s *gcService) StartWorker(ctx context.Context) {
	if !s.config.Enabled {
		s.log.Info("gc reconcile worker disabled")
		return
	}

	interval := s.config.Interval
	if interval <= 0 {
		interval = 1 * time.Hour
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		s.log.Info("gc reconcile worker started",
			zap.Duration("interval", interval),
			zap.Int("batch_size", s.config.BatchSize))

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				s.log.Info("gc reconcile worker stopped")
				return
			case <-ticker.C:
				s.runSweep(ctx)
			}
		}
	}()
}

// runSweep performs one worker tick: it publishes a heartbeat (Report refreshes
// the orphan gauges, so the worker's liveness is observable even when there is
// nothing to delete) and then runs a guarded reconcile. The last-run timestamp
// is advanced only when a reconcile actually ran on this replica — a
// lock-skipped tick leaves it untouched so a stale timestamp on a replica whose
// peer has died is detectable.
func (s *gcService) runSweep(ctx context.Context) {
	if _, err := s.Report(ctx); err != nil {
		s.log.Warn("gc heartbeat: failed to refresh orphan report", zap.Error(err))
	}

	limit := s.config.BatchSize
	if limit <= 0 {
		limit = 1000
	}

	result, err := s.reconcileLocked(ctx, limit)
	if err != nil {
		s.log.Error("gc reconcile sweep failed", zap.Error(err))
		return
	}
	if result.LockSkipped {
		return
	}
	metrics.GCLastRunTimestamp.Set(float64(time.Now().Unix()))
}

// StopWorker stops the background reconcile worker.
func (s *gcService) StopWorker() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// Ensure interface compliance.
var _ core.GCService = (*gcService)(nil)
