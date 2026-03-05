package core

import (
	"context"
	"time"
)

// ReplicationStatus represents the status of a replication job.
type ReplicationStatus string

const (
	ReplicationStatusPending    ReplicationStatus = "pending"
	ReplicationStatusInProgress ReplicationStatus = "in_progress"
	ReplicationStatusComplete   ReplicationStatus = "complete"
	ReplicationStatusFailed     ReplicationStatus = "failed"
)

// ReplicationRegion represents a storage region for replication.
type ReplicationRegion struct {
	ID          string `json:"id"`           // Unique region identifier (e.g., "us-east-1", "eu-west-1")
	Name        string `json:"name"`         // Human-readable name
	Endpoint    string `json:"endpoint"`     // S3-compatible endpoint URL
	Bucket      string `json:"bucket"`       // Bucket name
	AccessKey   string `json:"-"`            // Access key (not serialized)
	SecretKey   string `json:"-"`            // Secret key (not serialized)
	Region      string `json:"region"`       // AWS region
	Priority    int    `json:"priority"`     // Lower = higher priority for reads
	Enabled     bool   `json:"enabled"`      // Whether this region is active
	HealthCheck bool   `json:"health_check"` // Whether health checks are enabled
}

// ReplicationJob represents a blob replication job.
type ReplicationJob struct {
	ID          string            `json:"id"`
	BlobHash    string            `json:"blob_hash"`
	SourceRegion string           `json:"source_region"`
	TargetRegion string           `json:"target_region"`
	Status      ReplicationStatus `json:"status"`
	Progress    float64           `json:"progress"` // 0-100
	Error       string            `json:"error,omitempty"`
	CreatedAt   int64             `json:"created_at"`
	StartedAt   int64             `json:"started_at,omitempty"`
	CompletedAt int64             `json:"completed_at,omitempty"`
	Retries     int               `json:"retries"`
}

// BlobRegionStatus represents which regions have a copy of a blob.
type BlobRegionStatus struct {
	BlobHash  string   `json:"blob_hash"`
	Regions   []string `json:"regions"`    // Regions that have this blob
	Primary   string   `json:"primary"`    // Primary region
	Replicas  int      `json:"replicas"`   // Number of replicas
	Healthy   bool     `json:"healthy"`    // Whether replication is healthy
}

// ReplicationConfig contains configuration for multi-region replication.
type ReplicationConfig struct {
	Enabled         bool                `yaml:"enabled"`          // Enable multi-region replication
	PrimaryRegion   string              `yaml:"primary_region"`   // Primary region ID
	Regions         []ReplicationRegion `yaml:"regions"`          // All available regions
	ReplicaCount    int                 `yaml:"replica_count"`    // Target number of replicas (default: 2)
	SyncMode        string              `yaml:"sync_mode"`        // "async" or "sync"
	RetryAttempts   int                 `yaml:"retry_attempts"`   // Max retry attempts (default: 3)
	RetryDelay      time.Duration       `yaml:"retry_delay"`      // Delay between retries (default: 1m)
	WorkerCount     int                 `yaml:"worker_count"`     // Number of replication workers (default: 4)
	BatchSize       int                 `yaml:"batch_size"`       // Jobs per batch (default: 100)
	HealthCheckInterval time.Duration   `yaml:"health_check_interval"` // Region health check interval (default: 1m)
}

// DefaultReplicationConfig returns sensible defaults.
func DefaultReplicationConfig() ReplicationConfig {
	return ReplicationConfig{
		Enabled:             false,
		ReplicaCount:        2,
		SyncMode:            "async",
		RetryAttempts:       3,
		RetryDelay:          1 * time.Minute,
		WorkerCount:         4,
		BatchSize:           100,
		HealthCheckInterval: 1 * time.Minute,
	}
}

// ReplicationService handles multi-region blob replication.
type ReplicationService interface {
	// ReplicateBlob schedules a blob for replication to all configured regions.
	ReplicateBlob(ctx context.Context, hash string) error

	// ReplicateBlobToRegion schedules replication to a specific region.
	ReplicateBlobToRegion(ctx context.Context, hash string, targetRegion string) (*ReplicationJob, error)

	// GetReplicationStatus returns the replication status for a blob.
	GetReplicationStatus(ctx context.Context, hash string) (*BlobRegionStatus, error)

	// GetJob returns a specific replication job.
	GetJob(ctx context.Context, jobID string) (*ReplicationJob, error)

	// GetPendingJobs returns pending replication jobs.
	GetPendingJobs(ctx context.Context, limit int) ([]ReplicationJob, error)

	// CancelJob cancels a pending replication job.
	CancelJob(ctx context.Context, jobID string) error

	// GetRegions returns all configured regions.
	GetRegions(ctx context.Context) ([]ReplicationRegion, error)

	// GetHealthyRegions returns only healthy regions.
	GetHealthyRegions(ctx context.Context) ([]ReplicationRegion, error)

	// GetBestRegion returns the best region for reading a blob.
	// Takes into account region priority and health.
	GetBestRegion(ctx context.Context, hash string) (*ReplicationRegion, error)

	// SyncRegion triggers a full sync of a region.
	SyncRegion(ctx context.Context, regionID string) error

	// Start starts the replication workers.
	Start(ctx context.Context)

	// Stop stops the replication workers.
	Stop()

	// Stats returns replication statistics.
	Stats(ctx context.Context) (*ReplicationStats, error)
}

// ReplicationStats contains replication statistics.
type ReplicationStats struct {
	TotalJobs       int64            `json:"total_jobs"`
	PendingJobs     int64            `json:"pending_jobs"`
	InProgressJobs  int64            `json:"in_progress_jobs"`
	CompletedJobs   int64            `json:"completed_jobs"`
	FailedJobs      int64            `json:"failed_jobs"`
	AvgReplicationTime time.Duration `json:"avg_replication_time"`
	RegionStats     map[string]*RegionStats `json:"region_stats"`
}

// RegionStats contains statistics for a specific region.
type RegionStats struct {
	RegionID    string `json:"region_id"`
	BlobCount   int64  `json:"blob_count"`
	TotalSize   int64  `json:"total_size"`
	Healthy     bool   `json:"healthy"`
	LastChecked int64  `json:"last_checked"`
	Latency     time.Duration `json:"latency"` // Average latency to region
}
