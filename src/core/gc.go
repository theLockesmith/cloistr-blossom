package core

import (
	"context"
	"time"
)

// GCReport summarizes reference-bookkeeping drift found by a reconciliation
// scan. Deletes are reference-counted by design, but a crash between the
// separate DeleteBlobReference / DecrementBlobRefCount / DeleteFromHash steps,
// or the historical expiration path that bypassed ref_count, can leave blobs
// whose bookkeeping no longer matches reality.
type GCReport struct {
	// ZeroRefBlobs is the count of blobs whose ref_count has reached 0 or below
	// but whose row still exists.
	ZeroRefBlobs int64 `json:"zero_ref_blobs"`
	// OwnerlessBlobs is the count of blobs with no blob_references row at all.
	// This is the authoritative "no owner" signal and the set Reconcile targets.
	OwnerlessBlobs int64 `json:"ownerless_blobs"`
}

// GCReconcileResult reports what a reconcile run did (or, in dry-run mode, would
// have done).
type GCReconcileResult struct {
	DryRun         bool  `json:"dry_run"`
	OwnerlessFound int64 `json:"ownerless_found"`
	Deleted        int64 `json:"deleted"`
	// Hashes lists the blobs this run acted on: in dry-run mode, those that WOULD
	// be deleted; in a wet run, those whose DB row was removed (a blob skipped
	// because a new owner arrived, or whose row delete failed, is not included).
	// So len(Hashes) == Deleted for wet runs. Note "deleted" means the DB row is
	// gone; removal of the physical storage object is best-effort, so a listed
	// hash may still have an orphaned object (the S3 anti-join sweep catches it).
	Hashes []string `json:"hashes"`
	// LockSkipped is true when a non-dry-run reconcile could not acquire the
	// cross-replica advisory lock because another sweep (worker or operator) was
	// already running. Nothing was deleted.
	LockSkipped bool `json:"lock_skipped,omitempty"`
}

// GCConfig configures the automated garbage-collection worker.
type GCConfig struct {
	Enabled   bool          `yaml:"enabled"`    // Enable the background reconcile worker
	Interval  time.Duration `yaml:"interval"`   // How often to reconcile (default: 1h)
	BatchSize int           `yaml:"batch_size"` // Max ownerless blobs deleted per run (default: 1000)
}

// DefaultGCConfig returns sensible defaults. Enabled is false: automated
// deletion is opt-in per deployment (the production overlay flips it on) so a
// destructive sweep never auto-arms just by pulling a new image.
func DefaultGCConfig() GCConfig {
	return GCConfig{
		Enabled:   false,
		Interval:  1 * time.Hour,
		BatchSize: 1000,
	}
}

// GCService performs garbage-collection / reconciliation of orphaned blobs.
//
// Report is read-only. Reconcile deletes ownerless blobs; all deleting paths
// (the background worker and the operator endpoint) serialize on a single
// cross-replica advisory lock so at most one sweep runs at a time. When the
// worker is enabled it reconciles automatically on an interval; the operator
// endpoint remains available as an on-demand supplement and dry-run inspector.
type GCService interface {
	// Report returns current reference-bookkeeping drift counts. Read-only.
	Report(ctx context.Context) (*GCReport, error)
	// Reconcile deletes ownerless blobs (DB row + storage object), bounded by
	// limit. When dryRun is true it reports what would be deleted without
	// deleting anything (and without taking the advisory lock).
	Reconcile(ctx context.Context, dryRun bool, limit int) (*GCReconcileResult, error)
	// StartWorker starts the background reconcile worker. It is a no-op when
	// GCConfig.Enabled is false.
	StartWorker(ctx context.Context)
	// StopWorker stops the background reconcile worker.
	StopWorker()
}
