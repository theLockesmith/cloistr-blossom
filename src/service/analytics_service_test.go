package service

import (
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

func TestToInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int64
	}{
		{"int64", int64(42), 42},
		{"int32", int32(42), 42},
		{"int", int(42), 42},
		{"float64", float64(42.7), 42},
		{"nil", nil, 0},
		{"string", "not a number", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toInt64(tt.input)
			if result != tt.expected {
				t.Errorf("toInt64(%v) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCalculateGrowthPct(t *testing.T) {
	tests := []struct {
		name     string
		previous int64
		current  int64
		expected float64
	}{
		{"100% growth", 100, 200, 100.0},
		{"50% growth", 100, 150, 50.0},
		{"no change", 100, 100, 0.0},
		{"decline", 200, 100, -50.0},
		{"from zero with growth", 0, 100, 100.0},
		{"from zero no growth", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateGrowthPct(tt.previous, tt.current)
			if result != tt.expected {
				t.Errorf("calculateGrowthPct(%d, %d) = %f, want %f", tt.previous, tt.current, result, tt.expected)
			}
		})
	}
}

func TestDefaultAnalyticsQuery(t *testing.T) {
	query := core.DefaultAnalyticsQuery()

	// Should default to approximately 30 days (allow 1 hour tolerance for DST/rounding)
	expectedDuration := 30 * 24 * time.Hour
	actualDuration := query.EndTime.Sub(query.StartTime)

	// Allow 1 hour tolerance for test execution time and day boundary rounding
	if actualDuration < expectedDuration-time.Hour || actualDuration > expectedDuration+time.Hour {
		t.Errorf("DefaultAnalyticsQuery duration = %v, want approximately %v", actualDuration, expectedDuration)
	}

	if query.Bucket != core.TimeBucketDaily {
		t.Errorf("DefaultAnalyticsQuery bucket = %v, want %v", query.Bucket, core.TimeBucketDaily)
	}

	if query.Limit != 10 {
		t.Errorf("DefaultAnalyticsQuery limit = %d, want 10", query.Limit)
	}
}

func TestTimeBucketConstants(t *testing.T) {
	// Ensure constants have expected values
	if core.TimeBucketHourly != "hourly" {
		t.Errorf("TimeBucketHourly = %v, want hourly", core.TimeBucketHourly)
	}
	if core.TimeBucketDaily != "daily" {
		t.Errorf("TimeBucketDaily = %v, want daily", core.TimeBucketDaily)
	}
	if core.TimeBucketWeekly != "weekly" {
		t.Errorf("TimeBucketWeekly = %v, want weekly", core.TimeBucketWeekly)
	}
	if core.TimeBucketMonthly != "monthly" {
		t.Errorf("TimeBucketMonthly = %v, want monthly", core.TimeBucketMonthly)
	}
}

// TestAnalyticsServiceInterface ensures the service implements the interface.
func TestAnalyticsServiceInterface(t *testing.T) {
	// This is a compile-time check - if analyticsService doesn't implement
	// core.AnalyticsService, this won't compile.
	var _ core.AnalyticsService = (*analyticsService)(nil)
}

// Integration tests would require a test database.
// For now, we test the helper functions and ensure interfaces are satisfied.

func TestTimeSeriesPointJSON(t *testing.T) {
	point := core.TimeSeriesPoint{
		Timestamp: 1234567890,
		Value:     42,
	}

	if point.Timestamp != 1234567890 {
		t.Errorf("TimeSeriesPoint.Timestamp = %d, want 1234567890", point.Timestamp)
	}
	if point.Value != 42 {
		t.Errorf("TimeSeriesPoint.Value = %d, want 42", point.Value)
	}
}

func TestAnalyticsOverviewStruct(t *testing.T) {
	overview := core.AnalyticsOverview{
		TotalStorage:     1073741824, // 1 GB
		TotalBlobs:       100,
		TotalUsers:       50,
		StorageGrowth:    10.5,
		BlobGrowth:       5.0,
		UserGrowth:       2.5,
		UploadActivity:   15.0,
		DownloadActivity: 20.0,
		UploadsLast24h:   10,
		DownloadsLast24h: 25,
		BytesInLast24h:   104857600, // 100 MB
		BytesOutLast24h:  209715200, // 200 MB
		NewUsersLast24h:  5,
	}

	if overview.TotalStorage != 1073741824 {
		t.Errorf("AnalyticsOverview.TotalStorage = %d, want 1073741824", overview.TotalStorage)
	}
	if overview.StorageGrowth != 10.5 {
		t.Errorf("AnalyticsOverview.StorageGrowth = %f, want 10.5", overview.StorageGrowth)
	}
}
