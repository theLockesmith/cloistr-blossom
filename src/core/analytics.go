package core

import (
	"context"
	"time"
)

// TimeBucket represents a time period for analytics aggregation.
type TimeBucket string

const (
	TimeBucketHourly  TimeBucket = "hourly"
	TimeBucketDaily   TimeBucket = "daily"
	TimeBucketWeekly  TimeBucket = "weekly"
	TimeBucketMonthly TimeBucket = "monthly"
)

// TimeSeriesPoint represents a single data point in a time series.
type TimeSeriesPoint struct {
	Timestamp int64 `json:"timestamp"` // Unix timestamp
	Value     int64 `json:"value"`
}

// TimeSeries represents a series of time-bucketed data points.
type TimeSeries struct {
	Points []TimeSeriesPoint `json:"points"`
	Total  int64             `json:"total"`
}

// StorageAnalytics contains storage-related metrics over time.
type StorageAnalytics struct {
	TotalBytes       int64      `json:"total_bytes"`
	TotalBlobs       int64      `json:"total_blobs"`
	BytesOverTime    TimeSeries `json:"bytes_over_time"`
	BlobsOverTime    TimeSeries `json:"blobs_over_time"`
	DeduplicationPct float64    `json:"deduplication_pct"` // % storage saved via dedup
}

// ActivityAnalytics contains upload/download activity metrics.
type ActivityAnalytics struct {
	UploadsOverTime   TimeSeries `json:"uploads_over_time"`
	DownloadsOverTime TimeSeries `json:"downloads_over_time"`
	BytesUploaded     TimeSeries `json:"bytes_uploaded"`
	BytesDownloaded   TimeSeries `json:"bytes_downloaded"`
}

// UserAnalytics contains user-related metrics.
type UserAnalytics struct {
	TotalUsers       int64            `json:"total_users"`
	ActiveUsers      int64            `json:"active_users"` // Users with activity in period
	NewUsersOverTime TimeSeries       `json:"new_users_over_time"`
	TopUsers         []TopUser        `json:"top_users"`
	UsersByUsage     []UsageBucket    `json:"users_by_usage"` // Distribution histogram
}

// TopUser represents a user with high usage.
type TopUser struct {
	Pubkey     string `json:"pubkey"`
	UsedBytes  int64  `json:"used_bytes"`
	BlobCount  int64  `json:"blob_count"`
	LastActive int64  `json:"last_active"` // Unix timestamp
}

// UsageBucket represents a bucket in the usage distribution histogram.
type UsageBucket struct {
	MinBytes  int64 `json:"min_bytes"`
	MaxBytes  int64 `json:"max_bytes"`
	UserCount int64 `json:"user_count"`
}

// ContentAnalytics contains content type breakdown metrics.
type ContentAnalytics struct {
	TotalTypes    int64               `json:"total_types"`
	ByMimeType    []MimeTypeBreakdown `json:"by_mime_type"`
	ByCategory    []CategoryBreakdown `json:"by_category"` // image, video, audio, etc.
	EncryptionPct float64             `json:"encryption_pct"` // % of blobs encrypted
}

// MimeTypeBreakdown represents usage by MIME type.
type MimeTypeBreakdown struct {
	MimeType  string `json:"mime_type"`
	BlobCount int64  `json:"blob_count"`
	TotalSize int64  `json:"total_size"`
}

// CategoryBreakdown represents usage by content category.
type CategoryBreakdown struct {
	Category  string `json:"category"` // image, video, audio, document, other
	BlobCount int64  `json:"blob_count"`
	TotalSize int64  `json:"total_size"`
}

// AnalyticsOverview provides a high-level dashboard summary.
type AnalyticsOverview struct {
	// Current totals
	TotalStorage int64 `json:"total_storage"`
	TotalBlobs   int64 `json:"total_blobs"`
	TotalUsers   int64 `json:"total_users"`

	// Period comparisons (vs previous period)
	StorageGrowth   float64 `json:"storage_growth"`   // % change
	BlobGrowth      float64 `json:"blob_growth"`      // % change
	UserGrowth      float64 `json:"user_growth"`      // % change
	UploadActivity  float64 `json:"upload_activity"`  // % change
	DownloadActivity float64 `json:"download_activity"` // % change

	// Recent activity (last 24h)
	UploadsLast24h   int64 `json:"uploads_last_24h"`
	DownloadsLast24h int64 `json:"downloads_last_24h"`
	BytesInLast24h   int64 `json:"bytes_in_last_24h"`
	BytesOutLast24h  int64 `json:"bytes_out_last_24h"`
	NewUsersLast24h  int64 `json:"new_users_last_24h"`

	// Health indicators
	ErrorRate        float64 `json:"error_rate"`        // % of failed requests
	AvgResponseTime  float64 `json:"avg_response_time"` // ms
	RateLimitedCount int64   `json:"rate_limited_count"`
}

// AnalyticsQuery specifies the time range and granularity for analytics queries.
type AnalyticsQuery struct {
	StartTime  time.Time  // Start of query period
	EndTime    time.Time  // End of query period
	Bucket     TimeBucket // Time bucket size
	Limit      int        // Max results for top-N queries
}

// DefaultAnalyticsQuery returns a query for the last 30 days with daily buckets.
func DefaultAnalyticsQuery() AnalyticsQuery {
	now := time.Now()
	return AnalyticsQuery{
		StartTime: now.AddDate(0, 0, -30),
		EndTime:   now,
		Bucket:    TimeBucketDaily,
		Limit:     10,
	}
}

// AnalyticsService provides analytics and reporting functionality.
type AnalyticsService interface {
	// GetOverview returns a high-level dashboard summary.
	GetOverview(ctx context.Context) (*AnalyticsOverview, error)

	// GetStorageAnalytics returns storage metrics over time.
	GetStorageAnalytics(ctx context.Context, query AnalyticsQuery) (*StorageAnalytics, error)

	// GetActivityAnalytics returns upload/download activity over time.
	GetActivityAnalytics(ctx context.Context, query AnalyticsQuery) (*ActivityAnalytics, error)

	// GetUserAnalytics returns user-related metrics.
	GetUserAnalytics(ctx context.Context, query AnalyticsQuery) (*UserAnalytics, error)

	// GetContentAnalytics returns content type breakdown.
	GetContentAnalytics(ctx context.Context) (*ContentAnalytics, error)
}
