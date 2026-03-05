package core

import (
	"context"
	"time"
)

// ContentCategory represents categories of potentially harmful content.
type ContentCategory string

const (
	CategoryCSAM           ContentCategory = "csam"           // Child safety material (highest priority)
	CategoryExplicitAdult  ContentCategory = "explicit_adult" // Explicit adult content
	CategoryViolence       ContentCategory = "violence"       // Violent or graphic content
	CategoryHate           ContentCategory = "hate"           // Hate symbols or speech
	CategoryDrugs          ContentCategory = "drugs"          // Drug-related content
	CategoryWeapons        ContentCategory = "weapons"        // Weapons
	CategorySelfHarm       ContentCategory = "self_harm"      // Self-harm content
	CategorySpam           ContentCategory = "spam"           // Spam/scam content
	CategoryCopyrightRisk  ContentCategory = "copyright_risk" // Potential copyright violation
	CategorySafe           ContentCategory = "safe"           // No issues detected
)

// ScanConfidence represents the confidence level of a detection.
type ScanConfidence string

const (
	ConfidenceVeryLow  ScanConfidence = "very_low"  // <20%
	ConfidenceLow      ScanConfidence = "low"       // 20-50%
	ConfidenceMedium   ScanConfidence = "medium"    // 50-80%
	ConfidenceHigh     ScanConfidence = "high"      // 80-95%
	ConfidenceVeryHigh ScanConfidence = "very_high" // >95%
)

// ScanAction represents the recommended action based on scan results.
type ScanAction string

const (
	ScanActionAllow      ScanAction = "allow"      // Content is safe, allow upload
	ScanActionFlag       ScanAction = "flag"       // Flag for human review
	ScanActionQuarantine ScanAction = "quarantine" // Hold pending review
	ScanActionBlock      ScanAction = "block"      // Block immediately
)

// ContentDetection represents a single detection from a scan.
type ContentDetection struct {
	Category    ContentCategory `json:"category"`
	Confidence  float64         `json:"confidence"`    // 0-100
	Description string          `json:"description"`   // Human-readable description
	Metadata    map[string]any  `json:"metadata"`      // Provider-specific metadata
}

// ScanResult represents the result of an AI content scan.
type ScanResult struct {
	Hash           string             `json:"hash"`
	Provider       string             `json:"provider"`
	Detections     []ContentDetection `json:"detections"`
	RecommendedAction ScanAction      `json:"recommended_action"`
	Confidence     ScanConfidence     `json:"confidence"`
	ScanDuration   time.Duration      `json:"scan_duration"`
	ScannedAt      int64              `json:"scanned_at"`
	Error          string             `json:"error,omitempty"`
}

// ScanRequest represents a request to scan content.
type ScanRequest struct {
	Hash     string   `json:"hash"`
	Data     []byte   `json:"-"`       // The actual content bytes
	MimeType string   `json:"mime_type"`
	Size     int64    `json:"size"`
	Pubkey   string   `json:"pubkey"`  // Uploader's pubkey
}

// AIProviderConfig represents configuration for an AI provider.
type AIProviderConfig struct {
	Name        string            `yaml:"name"`         // Provider name (aws, google, photodna, local)
	Enabled     bool              `yaml:"enabled"`
	APIKey      string            `yaml:"api_key"`
	APISecret   string            `yaml:"api_secret"`
	Endpoint    string            `yaml:"endpoint"`     // Custom endpoint for self-hosted
	Region      string            `yaml:"region"`       // AWS region, etc.
	ProjectID   string            `yaml:"project_id"`   // GCP project ID
	Options     map[string]string `yaml:"options"`      // Provider-specific options
	Priority    int               `yaml:"priority"`     // Lower = higher priority
	Categories  []ContentCategory `yaml:"categories"`   // Categories this provider handles
}

// AIModerationConfig contains configuration for AI content moderation.
type AIModerationConfig struct {
	Enabled              bool               `yaml:"enabled"`
	Providers            []AIProviderConfig `yaml:"providers"`
	ScanOnUpload         bool               `yaml:"scan_on_upload"`          // Scan during upload
	ScanAsync            bool               `yaml:"scan_async"`              // Async scanning for large files
	AsyncThresholdBytes  int64              `yaml:"async_threshold_bytes"`   // Size threshold for async
	BlockHighConfidence  bool               `yaml:"block_high_confidence"`   // Auto-block high confidence CSAM
	QuarantineMedium     bool               `yaml:"quarantine_medium"`       // Quarantine medium confidence
	FlagLowConfidence    bool               `yaml:"flag_low_confidence"`     // Flag low confidence for review
	MaxScanSizeBytes     int64              `yaml:"max_scan_size_bytes"`     // Don't scan files larger than this
	SupportedMimeTypes   []string           `yaml:"supported_mime_types"`    // MIME types to scan
	WorkerCount          int                `yaml:"worker_count"`            // Async scan workers
	RetryAttempts        int                `yaml:"retry_attempts"`
	RetryDelay           time.Duration      `yaml:"retry_delay"`
	CacheResults         bool               `yaml:"cache_results"`           // Cache scan results
	CacheTTL             time.Duration      `yaml:"cache_ttl"`
}

// DefaultAIModerationConfig returns sensible defaults.
func DefaultAIModerationConfig() AIModerationConfig {
	return AIModerationConfig{
		Enabled:             false,
		ScanOnUpload:        true,
		ScanAsync:           true,
		AsyncThresholdBytes: 10 * 1024 * 1024, // 10 MB
		BlockHighConfidence: true,
		QuarantineMedium:    true,
		FlagLowConfidence:   true,
		MaxScanSizeBytes:    100 * 1024 * 1024, // 100 MB
		SupportedMimeTypes: []string{
			"image/jpeg",
			"image/png",
			"image/gif",
			"image/webp",
			"video/mp4",
			"video/webm",
			"video/quicktime",
		},
		WorkerCount:   2,
		RetryAttempts: 3,
		RetryDelay:    5 * time.Second,
		CacheResults:  true,
		CacheTTL:      24 * time.Hour,
	}
}

// ScanQueueItem represents an item in the async scan queue.
type ScanQueueItem struct {
	ID        string    `json:"id"`
	Hash      string    `json:"hash"`
	Pubkey    string    `json:"pubkey"`
	MimeType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	Priority  int       `json:"priority"`  // Lower = higher priority (CSAM reports get 0)
	CreatedAt int64     `json:"created_at"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
}

// QuarantinedBlob represents a blob held for review.
type QuarantinedBlob struct {
	Hash        string           `json:"hash"`
	Pubkey      string           `json:"pubkey"`
	MimeType    string           `json:"mime_type"`
	Size        int64            `json:"size"`
	ScanResult  *ScanResult      `json:"scan_result"`
	Status      string           `json:"status"` // pending, approved, rejected
	ReviewedBy  string           `json:"reviewed_by,omitempty"`
	CreatedAt   int64            `json:"created_at"`
	ReviewedAt  int64            `json:"reviewed_at,omitempty"`
}

// AIScanStats contains statistics about AI moderation.
type AIScanStats struct {
	TotalScans       int64            `json:"total_scans"`
	ScansToday       int64            `json:"scans_today"`
	BlockedCount     int64            `json:"blocked_count"`
	QuarantinedCount int64            `json:"quarantined_count"`
	FlaggedCount     int64            `json:"flagged_count"`
	AllowedCount     int64            `json:"allowed_count"`
	AvgScanTime      time.Duration    `json:"avg_scan_time"`
	QueueSize        int64            `json:"queue_size"`
	ProviderStats    map[string]int64 `json:"provider_stats"` // Scans per provider
	CategoryStats    map[string]int64 `json:"category_stats"` // Detections per category
}

// AIContentProvider is the interface for AI content moderation providers.
type AIContentProvider interface {
	// Name returns the provider name.
	Name() string

	// SupportedMimeTypes returns MIME types this provider can scan.
	SupportedMimeTypes() []string

	// SupportedCategories returns content categories this provider detects.
	SupportedCategories() []ContentCategory

	// Scan performs content analysis and returns detections.
	Scan(ctx context.Context, req *ScanRequest) (*ScanResult, error)

	// IsAvailable checks if the provider is operational.
	IsAvailable(ctx context.Context) bool
}

// AIModerationService handles AI-based content moderation.
type AIModerationService interface {
	// ScanContent scans content for harmful material.
	// Returns the scan result with recommended action.
	ScanContent(ctx context.Context, req *ScanRequest) (*ScanResult, error)

	// ScanContentAsync queues content for async scanning.
	// Returns immediately with a queue ID.
	ScanContentAsync(ctx context.Context, req *ScanRequest) (string, error)

	// GetScanResult retrieves a cached scan result.
	GetScanResult(ctx context.Context, hash string) (*ScanResult, error)

	// Quarantine operations

	// QuarantineBlob places a blob in quarantine pending review.
	QuarantineBlob(ctx context.Context, hash, pubkey string, scanResult *ScanResult) error

	// GetQuarantinedBlob returns a quarantined blob by hash.
	GetQuarantinedBlob(ctx context.Context, hash string) (*QuarantinedBlob, error)

	// ListQuarantinedBlobs returns blobs pending review.
	ListQuarantinedBlobs(ctx context.Context, status string, limit, offset int) ([]*QuarantinedBlob, error)

	// ReviewQuarantinedBlob approves or rejects a quarantined blob.
	ReviewQuarantinedBlob(ctx context.Context, hash string, approved bool, reviewerPubkey string) error

	// Queue operations

	// GetQueueItem returns an item from the scan queue.
	GetQueueItem(ctx context.Context, id string) (*ScanQueueItem, error)

	// GetQueueSize returns the current queue size.
	GetQueueSize(ctx context.Context) (int64, error)

	// Provider operations

	// RegisterProvider adds a content analysis provider.
	RegisterProvider(provider AIContentProvider)

	// GetProviders returns registered providers.
	GetProviders() []AIContentProvider

	// ShouldScan determines if content should be scanned based on config.
	ShouldScan(mimeType string, size int64) bool

	// DetermineAction determines the action based on scan results and config.
	DetermineAction(result *ScanResult) ScanAction

	// Statistics

	// GetStats returns AI moderation statistics.
	GetStats(ctx context.Context) (*AIScanStats, error)

	// Worker management

	// Start starts the async scan workers.
	Start(ctx context.Context)

	// Stop stops the workers.
	Stop()

	// IsEnabled returns whether AI moderation is enabled.
	IsEnabled() bool
}

// ConfidenceToLevel converts a numeric confidence to a level.
func ConfidenceToLevel(confidence float64) ScanConfidence {
	switch {
	case confidence >= 95:
		return ConfidenceVeryHigh
	case confidence >= 80:
		return ConfidenceHigh
	case confidence >= 50:
		return ConfidenceMedium
	case confidence >= 20:
		return ConfidenceLow
	default:
		return ConfidenceVeryLow
	}
}

// CategoryToReportReason maps content categories to report reasons.
func CategoryToReportReason(category ContentCategory) ReportReason {
	switch category {
	case CategoryCSAM:
		return ReportReasonCSAM
	case CategoryExplicitAdult, CategoryViolence, CategoryHate, CategorySelfHarm:
		return ReportReasonAbuse
	case CategoryCopyrightRisk:
		return ReportReasonCopyright
	case CategoryDrugs, CategoryWeapons:
		return ReportReasonIllegal
	default:
		return ReportReasonOther
	}
}

// IsCriticalCategory returns true for categories requiring immediate action.
func IsCriticalCategory(category ContentCategory) bool {
	return category == CategoryCSAM
}
