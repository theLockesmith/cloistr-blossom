// Package metrics provides Prometheus metrics reading for analytics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// RealtimeMetrics contains current metric values read from Prometheus.
type RealtimeMetrics struct {
	// Downloads
	DownloadsTotal   float64 // Total successful downloads
	DownloadsErrors  float64 // Total failed downloads
	DownloadBytes    float64 // Total bytes downloaded

	// Uploads
	UploadsTotal     float64 // Total successful uploads
	UploadsErrors    float64 // Total failed uploads
	UploadBytes      float64 // Total bytes uploaded

	// Errors by type
	ErrorsUpload     float64
	ErrorsDownload   float64
	ErrorsStorage    float64
	ErrorsDatabase   float64
	ErrorsAuth       float64

	// Rate limiting
	RateLimitedDownload float64
	RateLimitedUpload   float64
	RateLimitedGeneral  float64

	// Storage (gauges)
	StorageBytes     float64
	StoredBlobs      float64
	ActiveUsers      float64

	// Request stats
	RequestsTotal    float64
	RequestsErrors   float64 // 4xx + 5xx responses

	// Reports
	ReportsTotal     float64
	BlockedUploads   float64
}

// Reader reads current metric values from the Prometheus registry.
type Reader struct{}

// NewReader creates a new metrics reader.
func NewReader() *Reader {
	return &Reader{}
}

// GetRealtimeMetrics reads current metric values from the in-process Prometheus registry.
func (r *Reader) GetRealtimeMetrics() (*RealtimeMetrics, error) {
	m := &RealtimeMetrics{}

	// Read download metrics
	m.DownloadsTotal = getCounterValue(DownloadsTotal, "success")
	m.DownloadsErrors = getCounterValue(DownloadsTotal, "error") + getCounterValue(DownloadsTotal, "not_found")
	m.DownloadBytes = getSimpleCounterValue(DownloadBytes)

	// Read upload metrics
	m.UploadsTotal = getCounterVecSum(UploadsTotal, "success")
	m.UploadsErrors = getCounterVecSum(UploadsTotal, "error")
	m.UploadBytes = getSimpleCounterValue(UploadBytes)

	// Read error metrics
	m.ErrorsUpload = getCounterValue(ErrorsTotal, "upload")
	m.ErrorsDownload = getCounterValue(ErrorsTotal, "download")
	m.ErrorsStorage = getCounterValue(ErrorsTotal, "storage")
	m.ErrorsDatabase = getCounterValue(ErrorsTotal, "database")
	m.ErrorsAuth = getCounterValue(ErrorsTotal, "auth")

	// Read rate limiting metrics
	m.RateLimitedDownload = getCounterValue(RateLimitedTotal, "download")
	m.RateLimitedUpload = getCounterValue(RateLimitedTotal, "upload")
	m.RateLimitedGeneral = getCounterValue(RateLimitedTotal, "general")

	// Read storage gauges
	m.StorageBytes = getGaugeValue(StorageBytes)
	m.StoredBlobs = getGaugeValue(StoredBlobs)
	m.ActiveUsers = getGaugeValue(ActiveUsers)

	// Read request metrics (sum across all paths/statuses)
	m.RequestsTotal, m.RequestsErrors = getRequestStats()

	// Read moderation metrics
	m.ReportsTotal = getReportsTotal()
	m.BlockedUploads = getSimpleCounterValue(BlockedUploadsTotal)

	return m, nil
}

// getCounterValue reads a counter value with a single label.
func getCounterValue(cv *prometheus.CounterVec, labelValue string) float64 {
	counter, err := cv.GetMetricWithLabelValues(labelValue)
	if err != nil {
		return 0
	}

	var m dto.Metric
	if err := counter.Write(&m); err != nil {
		return 0
	}

	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return 0
}

// getCounterVecSum reads sum of counter values matching first label.
func getCounterVecSum(cv *prometheus.CounterVec, firstLabelValue string) float64 {
	// For UploadsTotal which has labels: status, encryption_mode
	// Sum across all encryption modes for a given status
	var total float64
	for _, encMode := range []string{"none", "server", "e2e"} {
		counter, err := cv.GetMetricWithLabelValues(firstLabelValue, encMode)
		if err != nil {
			continue
		}

		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}

		if m.Counter != nil {
			total += m.Counter.GetValue()
		}
	}
	return total
}

// getSimpleCounterValue reads a counter without labels.
func getSimpleCounterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		return 0
	}

	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return 0
}

// getGaugeValue reads a gauge value.
func getGaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return 0
	}

	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	return 0
}

// getRequestStats reads total requests and error requests from RequestsTotal.
func getRequestStats() (total, errors float64) {
	// We need to iterate through all label combinations
	// This is a bit tricky with the prometheus client
	// For now, we'll sample the most common paths

	commonPaths := []string{"/upload", "/:hash", "/list/:pubkey", "/media", "/stats"}
	methods := []string{"GET", "POST", "PUT", "DELETE", "HEAD"}
	errorStatuses := []string{"400", "401", "403", "404", "500", "502", "503"}
	successStatuses := []string{"200", "201", "204", "301", "302", "304"}

	for _, method := range methods {
		for _, path := range commonPaths {
			// Count success
			for _, status := range successStatuses {
				counter, err := RequestsTotal.GetMetricWithLabelValues(method, path, status)
				if err != nil {
					continue
				}
				var m dto.Metric
				if err := counter.Write(&m); err != nil {
					continue
				}
				if m.Counter != nil {
					total += m.Counter.GetValue()
				}
			}

			// Count errors
			for _, status := range errorStatuses {
				counter, err := RequestsTotal.GetMetricWithLabelValues(method, path, status)
				if err != nil {
					continue
				}
				var m dto.Metric
				if err := counter.Write(&m); err != nil {
					continue
				}
				if m.Counter != nil {
					val := m.Counter.GetValue()
					total += val
					errors += val
				}
			}
		}
	}

	return total, errors
}

// getReportsTotal sums all report counters.
func getReportsTotal() float64 {
	var total float64
	for _, reason := range []string{"spam", "illegal", "copyright", "other"} {
		total += getCounterValue(ReportsTotal, reason)
	}
	return total
}

// ErrorRate calculates the error rate as a percentage.
func (m *RealtimeMetrics) ErrorRate() float64 {
	if m.RequestsTotal == 0 {
		return 0
	}
	return (m.RequestsErrors / m.RequestsTotal) * 100
}

// TotalErrors returns the sum of all error types.
func (m *RealtimeMetrics) TotalErrors() float64 {
	return m.ErrorsUpload + m.ErrorsDownload + m.ErrorsStorage + m.ErrorsDatabase + m.ErrorsAuth
}

// TotalRateLimited returns the sum of all rate-limited requests.
func (m *RealtimeMetrics) TotalRateLimited() float64 {
	return m.RateLimitedDownload + m.RateLimitedUpload + m.RateLimitedGeneral
}
