// Package metrics provides Prometheus metrics for coldforge-blossom.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "coldforge_blossom"

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
)
