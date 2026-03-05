package core

import (
	"context"
	"time"
)

// ExpirationPolicy defines a rule for auto-expiring blobs.
type ExpirationPolicy struct {
	ID         int32  `json:"id"`
	Name       string `json:"name"`
	MimePrefix string `json:"mime_prefix,omitempty"` // MIME type prefix to match
	TTLSeconds int32  `json:"ttl_seconds"`           // Time-to-live in seconds
	MaxSize    int64  `json:"max_size,omitempty"`    // Only apply to blobs under this size
	Pubkey     string `json:"pubkey,omitempty"`      // Only apply to specific pubkey
	Priority   int32  `json:"priority"`              // Higher priority takes precedence
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// ExpirationConfig contains configuration for blob expiration.
type ExpirationConfig struct {
	Enabled         bool          `yaml:"enabled"`           // Enable auto-expiration
	CleanupInterval time.Duration `yaml:"cleanup_interval"`  // How often to run cleanup (default: 1h)
	BatchSize       int           `yaml:"batch_size"`        // Max blobs to delete per cleanup run (default: 1000)
	DefaultTTL      time.Duration `yaml:"default_ttl"`       // Default TTL for new blobs (0 = never expire)
	GracePeriod     time.Duration `yaml:"grace_period"`      // Grace period before actual deletion (default: 0)
}

// DefaultExpirationConfig returns sensible defaults.
func DefaultExpirationConfig() ExpirationConfig {
	return ExpirationConfig{
		Enabled:         false,
		CleanupInterval: 1 * time.Hour,
		BatchSize:       1000,
		DefaultTTL:      0, // Never expire by default
		GracePeriod:     0,
	}
}

// ExpirationService handles blob expiration and cleanup.
type ExpirationService interface {
	// SetExpiration sets the expiration time for a blob.
	SetExpiration(ctx context.Context, hash string, expiresAt time.Time) error

	// SetExpirationTTL sets the expiration time relative to now.
	SetExpirationTTL(ctx context.Context, hash string, ttl time.Duration) error

	// ClearExpiration removes expiration from a blob.
	ClearExpiration(ctx context.Context, hash string) error

	// GetExpiredBlobs returns blobs that have expired.
	GetExpiredBlobs(ctx context.Context, limit int) ([]ExpiredBlob, error)

	// CleanupExpired deletes expired blobs and returns the count.
	CleanupExpired(ctx context.Context) (int, error)

	// CountExpired returns the number of expired blobs pending deletion.
	CountExpired(ctx context.Context) (int64, error)

	// ApplyPolicy applies an expiration policy to a blob if it matches.
	// Returns true if a policy was applied.
	ApplyPolicy(ctx context.Context, hash string, mimeType string, size int64, pubkey string) (bool, error)

	// GetPolicies returns all enabled expiration policies.
	GetPolicies(ctx context.Context) ([]ExpirationPolicy, error)

	// CreatePolicy creates a new expiration policy.
	CreatePolicy(ctx context.Context, policy *ExpirationPolicy) (*ExpirationPolicy, error)

	// UpdatePolicy updates an existing policy.
	UpdatePolicy(ctx context.Context, policy *ExpirationPolicy) error

	// DeletePolicy removes a policy.
	DeletePolicy(ctx context.Context, id int32) error

	// StartCleanupWorker starts the background cleanup worker.
	StartCleanupWorker(ctx context.Context)

	// StopCleanupWorker stops the background cleanup worker.
	StopCleanupWorker()
}

// ExpiredBlob contains info about an expired blob pending deletion.
type ExpiredBlob struct {
	Hash    string `json:"hash"`
	Pubkey  string `json:"pubkey"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Created int64  `json:"created"`
}
