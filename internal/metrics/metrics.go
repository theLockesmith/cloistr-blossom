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

	// Payment metrics (BUD-07)

	// PaymentRequestsTotal counts payment requests by method.
	PaymentRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payment_requests_total",
			Help:      "Total payment requests created by method",
		},
		[]string{"method"},
	)

	// PaymentsVerifiedTotal counts verified payments by method.
	PaymentsVerifiedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payments_verified_total",
			Help:      "Total verified payments by method",
		},
		[]string{"method"},
	)

	// PaymentRequiredTotal counts 402 Payment Required responses.
	PaymentRequiredTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payment_required_total",
			Help:      "Total 402 Payment Required responses",
		},
	)

	// PaymentSatsReceived tracks total satoshis received.
	PaymentSatsReceived = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payment_sats_received_total",
			Help:      "Total satoshis received from payments",
		},
	)

	// FreeTierUploadsTotal counts uploads within free tier.
	FreeTierUploadsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "free_tier_uploads_total",
			Help:      "Total uploads within free tier allowance",
		},
	)

	// FreeTierBytesUsed tracks bytes consumed from free tier.
	FreeTierBytesUsed = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "free_tier_bytes_used_total",
			Help:      "Total bytes consumed from free tier allowance",
		},
	)

	// Expiration worker metrics

	// ExpirationSweepsTotal counts cleanup worker runs by outcome.
	// result is one of: "ok", "error", "skipped_locked" (another replica held
	// the advisory lock).
	ExpirationSweepsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "expiration_sweeps_total",
			Help:      "Total expiration cleanup worker runs by outcome",
		},
		[]string{"result"},
	)

	// ExpiredBlobsDeletedTotal counts blobs deleted by the expiration worker.
	ExpiredBlobsDeletedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "expired_blobs_deleted_total",
			Help:      "Total blobs deleted by the expiration cleanup worker",
		},
	)

	// PendingExpiredBlobs tracks the number of expired-but-not-yet-deleted blobs.
	PendingExpiredBlobs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "pending_expired_blobs",
			Help:      "Number of expired blobs awaiting deletion",
		},
	)

	// ExpirationLastRunTimestamp is the Unix time of the last completed sweep.
	ExpirationLastRunTimestamp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "expiration_last_run_timestamp_seconds",
			Help:      "Unix timestamp of the last completed expiration sweep",
		},
	)

	// Garbage-collection / reconciliation metrics

	// OrphanedBlobs tracks blobs whose reference bookkeeping has drifted.
	// kind is one of: "zero_ref" (ref_count <= 0) or "ownerless" (no
	// blob_references row). Updated whenever a reconcile report runs.
	OrphanedBlobs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "orphaned_blobs",
			Help:      "Blobs with drifted reference bookkeeping by kind",
		},
		[]string{"kind"},
	)

	// GCReconciledTotal counts blobs deleted by reconcile runs (worker + manual).
	GCReconciledTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "gc_reconciled_total",
			Help:      "Total orphaned blobs deleted by reconcile runs",
		},
	)

	// GCSweepsTotal counts GC reconcile runs (background worker and manual
	// operator reconciles both route through the locked path) by outcome.
	// result is one of: "ok", "error", "skipped_locked" (another sweep held the
	// advisory lock).
	GCSweepsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "gc_sweeps_total",
			Help:      "Total GC reconcile runs (worker and manual) by outcome",
		},
		[]string{"result"},
	)

	// GCLastRunTimestamp is the Unix time of the last completed GC sweep.
	GCLastRunTimestamp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "gc_last_run_timestamp_seconds",
			Help:      "Unix timestamp of the last completed GC reconcile sweep",
		},
	)

	// MirrorRequests counts media-mirror requests by outcome.
	//
	// Note what is NOT a label here: no URL, no host, no pubkey, no IP. A
	// Prometheus label is a permanent, scrapeable, per-series record, so
	// labelling this by URL would rebuild the viewing log the mirror exists to
	// avoid -- and would blow up cardinality besides. Outcome only.
	MirrorRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "media_mirror_requests_total",
			Help:      "Media mirror requests by outcome (hit, miss, refused, unreachable, ...)",
		},
		[]string{"result"},
	)

	// MirrorBytes tracks bytes newly mirrored from remote hosts.
	MirrorBytes = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "media_mirror_fetched_bytes_total",
			Help:      "Total bytes fetched from remote hosts by the media mirror",
		},
	)

	// MirrorEvictions counts objects dropped by the LRU eviction worker.
	MirrorEvictions = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "media_mirror_evictions_total",
			Help:      "Objects evicted from the media mirror cache",
		},
	)

	// MirrorCacheBytes is the current total size of the media mirror cache.
	MirrorCacheBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "media_mirror_cache_bytes",
			Help:      "Current size in bytes of the media mirror cache",
		},
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

	// Initialize payment counters
	PaymentRequestsTotal.WithLabelValues("lightning").Add(0)
	PaymentRequestsTotal.WithLabelValues("cashu").Add(0)
	PaymentsVerifiedTotal.WithLabelValues("lightning").Add(0)
	PaymentsVerifiedTotal.WithLabelValues("cashu").Add(0)

	// Initialize expiration sweep counters
	ExpirationSweepsTotal.WithLabelValues("ok").Add(0)
	ExpirationSweepsTotal.WithLabelValues("error").Add(0)
	ExpirationSweepsTotal.WithLabelValues("skipped_locked").Add(0)

	// Initialize orphaned-blob gauges
	OrphanedBlobs.WithLabelValues("zero_ref").Set(0)
	OrphanedBlobs.WithLabelValues("ownerless").Set(0)

	// Initialize GC sweep counters
	GCSweepsTotal.WithLabelValues("ok").Add(0)
	GCSweepsTotal.WithLabelValues("error").Add(0)
	GCSweepsTotal.WithLabelValues("skipped_locked").Add(0)

	// Initialize media mirror counters
	MirrorRequests.WithLabelValues("hit").Add(0)
	MirrorRequests.WithLabelValues("miss").Add(0)
	MirrorRequests.WithLabelValues("refused").Add(0)
	MirrorRequests.WithLabelValues("unreachable").Add(0)
	MirrorRequests.WithLabelValues("refused_cached").Add(0)
	MirrorRequests.WithLabelValues("unreachable_cached").Add(0)
	MirrorCacheBytes.Set(0)
}
