// Package metrics provides Prometheus metrics for cloistr-blossom.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "cloistr_blossom"

var (
	// RequestsTotal counts total HTTP requests by method and status.
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_total",
			Help:      "Total HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	// RequestDuration tracks request latency in seconds.
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// ErrorsTotal counts errors by type.
	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "errors_total",
			Help:      "Total errors by type",
		},
		[]string{"type"},
	)

	// UploadsTotal counts blob uploads.
	UploadsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "uploads_total",
			Help:      "Total blob uploads",
		},
		[]string{"status", "encryption_mode"},
	)

	// UploadBytes tracks total bytes uploaded.
	UploadBytes = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "upload_bytes_total",
			Help:      "Total bytes uploaded",
		},
	)

	// DownloadsTotal counts blob downloads.
	DownloadsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "downloads_total",
			Help:      "Total blob downloads",
		},
		[]string{"status"},
	)

	// DownloadBytes tracks total bytes downloaded.
	DownloadBytes = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "download_bytes_total",
			Help:      "Total bytes downloaded",
		},
	)

	// ActiveUsers tracks unique pubkeys that have uploaded.
	ActiveUsers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_users",
			Help:      "Number of users with stored blobs",
		},
	)

	// StoredBlobs tracks total number of stored blobs.
	StoredBlobs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "stored_blobs",
			Help:      "Total number of stored blobs",
		},
	)

	// StorageBytes tracks total storage used in bytes.
	StorageBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "storage_bytes",
			Help:      "Total storage used in bytes",
		},
	)

	// ReportsTotal counts content reports by reason.
	ReportsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reports_total",
			Help:      "Total content reports by reason",
		},
		[]string{"reason"},
	)

	// BlockedUploadsTotal counts uploads blocked due to blocklist.
	BlockedUploadsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "blocked_uploads_total",
			Help:      "Total uploads blocked due to blocklist",
		},
	)

	// RateLimitedTotal counts rate-limited requests by type.
	RateLimitedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limited_total",
			Help:      "Total rate-limited requests by type",
		},
		[]string{"type"},
	)
)

// Init initializes all CounterVec metrics with default label values.
// This ensures they appear in Prometheus output as 0 instead of "no data".
func Init() {
	// Initialize uploads counters
	UploadsTotal.WithLabelValues("success", "none").Add(0)
	UploadsTotal.WithLabelValues("success", "server").Add(0)
	UploadsTotal.WithLabelValues("success", "e2e").Add(0)
	UploadsTotal.WithLabelValues("error", "none").Add(0)
	UploadsTotal.WithLabelValues("error", "server").Add(0)
	UploadsTotal.WithLabelValues("error", "e2e").Add(0)

	// Initialize downloads counters
	DownloadsTotal.WithLabelValues("success").Add(0)
	DownloadsTotal.WithLabelValues("error").Add(0)
	DownloadsTotal.WithLabelValues("not_found").Add(0)

	// Initialize errors counters
	ErrorsTotal.WithLabelValues("upload").Add(0)
	ErrorsTotal.WithLabelValues("download").Add(0)
	ErrorsTotal.WithLabelValues("storage").Add(0)
	ErrorsTotal.WithLabelValues("database").Add(0)
	ErrorsTotal.WithLabelValues("auth").Add(0)

	// Initialize reports counters
	ReportsTotal.WithLabelValues("spam").Add(0)
	ReportsTotal.WithLabelValues("illegal").Add(0)
	ReportsTotal.WithLabelValues("copyright").Add(0)
	ReportsTotal.WithLabelValues("other").Add(0)

	// Initialize requests counters with common status codes (for HTTP error rate calculation)
	commonPaths := []string{"/upload", "/metrics", "/.well-known/health", "/stats"}
	commonStatuses := []string{"200", "400", "401", "403", "404", "500", "502", "503"}
	for _, path := range commonPaths {
		for _, status := range commonStatuses {
			RequestsTotal.WithLabelValues("GET", path, status).Add(0)
		}
	}

	// Initialize rate limiting counters
	RateLimitedTotal.WithLabelValues("download").Add(0)
	RateLimitedTotal.WithLabelValues("upload").Add(0)
	RateLimitedTotal.WithLabelValues("general").Add(0)
	RateLimitedTotal.WithLabelValues("bandwidth_upload").Add(0)
	RateLimitedTotal.WithLabelValues("bandwidth_download").Add(0)
}
