package service

import (
	"context"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"go.uber.org/zap"
)

type analyticsService struct {
	queries *db.Queries
	log     *zap.Logger
}

// NewAnalyticsService creates a new analytics service.
func NewAnalyticsService(queries *db.Queries, log *zap.Logger) (core.AnalyticsService, error) {
	return &analyticsService{
		queries: queries,
		log:     log,
	}, nil
}

// toInt64 safely converts interface{} to int64.
func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int32:
		return int64(val)
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case nil:
		return 0
	default:
		return 0
	}
}

func (s *analyticsService) GetOverview(ctx context.Context) (*core.AnalyticsOverview, error) {
	now := time.Now()
	oneDayAgo := now.Add(-24 * time.Hour).Unix()
	oneWeekAgo := now.Add(-7 * 24 * time.Hour).Unix()
	twoWeeksAgo := now.Add(-14 * 24 * time.Hour).Unix()

	// Get current totals
	stats, err := s.queries.GetStats(ctx)
	if err != nil {
		return nil, err
	}

	userCount, err := s.queries.GetUserCount(ctx)
	if err != nil {
		s.log.Warn("failed to get user count", zap.Error(err))
		userCount = 0
	}

	// Get recent activity (last 24h)
	recentActivity, err := s.queries.GetRecentActivity(ctx, oneDayAgo)
	if err != nil {
		s.log.Warn("failed to get recent activity", zap.Error(err))
	}

	// Get storage from one week ago for growth calculation
	storageLastWeek, err := s.queries.GetStorageAtTime(ctx, oneWeekAgo)
	if err != nil {
		s.log.Warn("failed to get historical storage", zap.Error(err))
	}

	// Get active users for current and previous week
	activeThisWeek, err := s.queries.GetActiveUsersInPeriod(ctx, db.GetActiveUsersInPeriodParams{
		Created:   oneWeekAgo,
		Created_2: now.Unix(),
	})
	if err != nil {
		s.log.Warn("failed to get active users this week", zap.Error(err))
	}

	activeLastWeek, err := s.queries.GetActiveUsersInPeriod(ctx, db.GetActiveUsersInPeriodParams{
		Created:   twoWeeksAgo,
		Created_2: oneWeekAgo,
	})
	if err != nil {
		s.log.Warn("failed to get active users last week", zap.Error(err))
	}

	// Calculate growth percentages
	currentStorage := stats.BytesStored
	prevStorage := toInt64(storageLastWeek.TotalBytes) // TotalBytes is interface{} from COALESCE
	storageGrowth := calculateGrowthPct(prevStorage, currentStorage)

	currentBlobs := stats.BlobCount
	prevBlobs := storageLastWeek.BlobCount
	blobGrowth := calculateGrowthPct(prevBlobs, currentBlobs)

	// User growth (week over week)
	newUsersParams := db.GetNewUsersPerDayParams{
		CreatedAt:   oneWeekAgo,
		CreatedAt_2: now.Unix(),
	}
	newUsersThisWeek, _ := s.queries.GetNewUsersPerDay(ctx, newUsersParams)
	var newUsersThisWeekCount int64
	for _, row := range newUsersThisWeek {
		newUsersThisWeekCount += row.UserCount
	}

	newUsersLastWeekParams := db.GetNewUsersPerDayParams{
		CreatedAt:   twoWeeksAgo,
		CreatedAt_2: oneWeekAgo,
	}
	newUsersLastWeek, _ := s.queries.GetNewUsersPerDay(ctx, newUsersLastWeekParams)
	var newUsersLastWeekCount int64
	for _, row := range newUsersLastWeek {
		newUsersLastWeekCount += row.UserCount
	}
	userGrowth := calculateGrowthPct(newUsersLastWeekCount, newUsersThisWeekCount)

	// Activity growth (based on active users as a proxy for upload activity)
	activityGrowth := calculateGrowthPct(activeLastWeek, activeThisWeek)

	overview := &core.AnalyticsOverview{
		TotalStorage:     currentStorage,
		TotalBlobs:       currentBlobs,
		TotalUsers:       userCount,
		StorageGrowth:    storageGrowth,
		BlobGrowth:       blobGrowth,
		UserGrowth:       userGrowth,
		UploadActivity:   activityGrowth,
		DownloadActivity: 0, // Downloads not tracked in DB, would need Prometheus
		UploadsLast24h:   recentActivity.Uploads,
		DownloadsLast24h: 0, // Not tracked in DB
		BytesInLast24h:   toInt64(recentActivity.BytesUploaded),
		BytesOutLast24h:  0, // Not tracked in DB
		NewUsersLast24h:  recentActivity.NewUsers,
		ErrorRate:        0, // Would need Prometheus metrics
		AvgResponseTime:  0, // Would need Prometheus metrics
		RateLimitedCount: 0, // Would need Prometheus metrics
	}

	return overview, nil
}

func (s *analyticsService) GetStorageAnalytics(ctx context.Context, query core.AnalyticsQuery) (*core.StorageAnalytics, error) {
	startTs := query.StartTime.Unix()
	endTs := query.EndTime.Unix()

	// Get current totals
	stats, err := s.queries.GetStats(ctx)
	if err != nil {
		return nil, err
	}

	// Get storage growth over time
	growthData, err := s.queries.GetStorageGrowthDaily(ctx, db.GetStorageGrowthDailyParams{
		Created:   startTs,
		Created_2: endTs,
	})
	if err != nil {
		return nil, err
	}

	// Convert to time series
	bytesOverTime := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0, len(growthData))}
	blobsOverTime := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0, len(growthData))}

	for _, row := range growthData {
		bytesOverTime.Points = append(bytesOverTime.Points, core.TimeSeriesPoint{
			Timestamp: int64(row.BucketTimestamp),
			Value:     row.CumulativeBytes,
		})
		blobsOverTime.Points = append(blobsOverTime.Points, core.TimeSeriesPoint{
			Timestamp: int64(row.BucketTimestamp),
			Value:     row.CumulativeBlobs,
		})
	}

	bytesOverTime.Total = toInt64(stats.BytesStored)
	blobsOverTime.Total = stats.BlobCount

	// Get deduplication stats
	dedupStats, err := s.queries.GetDeduplicationStats(ctx)
	if err != nil {
		s.log.Warn("failed to get deduplication stats", zap.Error(err))
	}

	var dedupPct float64
	logicalStorage := toInt64(dedupStats.LogicalStorage)
	actualStorage := toInt64(dedupStats.ActualStorage)
	if logicalStorage > 0 && actualStorage > 0 {
		dedupPct = (1 - float64(actualStorage)/float64(logicalStorage)) * 100
		if dedupPct < 0 {
			dedupPct = 0
		}
	}

	return &core.StorageAnalytics{
		TotalBytes:       toInt64(stats.BytesStored),
		TotalBlobs:       stats.BlobCount,
		BytesOverTime:    bytesOverTime,
		BlobsOverTime:    blobsOverTime,
		DeduplicationPct: dedupPct,
	}, nil
}

func (s *analyticsService) GetActivityAnalytics(ctx context.Context, query core.AnalyticsQuery) (*core.ActivityAnalytics, error) {
	startTs := query.StartTime.Unix()
	endTs := query.EndTime.Unix()

	// Get uploads per day
	uploadsData, err := s.queries.GetUploadsPerDay(ctx, db.GetUploadsPerDayParams{
		Created:   startTs,
		Created_2: endTs,
	})
	if err != nil {
		return nil, err
	}

	// Note: GetReferencesPerDay could be used to track user upload actions
	// (which may differ from unique blob counts due to deduplication)

	// Convert uploads to time series
	uploadsOverTime := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0, len(uploadsData))}
	bytesUploaded := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0, len(uploadsData))}

	var totalUploads, totalBytes int64
	for _, row := range uploadsData {
		uploadsOverTime.Points = append(uploadsOverTime.Points, core.TimeSeriesPoint{
			Timestamp: int64(row.BucketTimestamp),
			Value:     row.UploadCount,
		})
		bytesVal := toInt64(row.TotalBytes)
		bytesUploaded.Points = append(bytesUploaded.Points, core.TimeSeriesPoint{
			Timestamp: int64(row.BucketTimestamp),
			Value:     bytesVal,
		})
		totalUploads += row.UploadCount
		totalBytes += bytesVal
	}

	uploadsOverTime.Total = totalUploads
	bytesUploaded.Total = totalBytes

	// References represent user download/access intentions (though we track uploads)
	// For downloads, we'd need Prometheus metrics - this is a placeholder
	downloadsOverTime := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0)}
	bytesDownloaded := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0)}

	// Note: True download stats would come from Prometheus metrics, not the database.

	return &core.ActivityAnalytics{
		UploadsOverTime:   uploadsOverTime,
		DownloadsOverTime: downloadsOverTime,
		BytesUploaded:     bytesUploaded,
		BytesDownloaded:   bytesDownloaded,
	}, nil
}

func (s *analyticsService) GetUserAnalytics(ctx context.Context, query core.AnalyticsQuery) (*core.UserAnalytics, error) {
	startTs := query.StartTime.Unix()
	endTs := query.EndTime.Unix()

	// Get total users
	totalUsers, err := s.queries.GetUserCount(ctx)
	if err != nil {
		return nil, err
	}

	// Get active users in the query period
	activeUsers, err := s.queries.GetActiveUsersInPeriod(ctx, db.GetActiveUsersInPeriodParams{
		Created:   startTs,
		Created_2: endTs,
	})
	if err != nil {
		s.log.Warn("failed to get active users", zap.Error(err))
	}

	// Get new users over time
	newUsersData, err := s.queries.GetNewUsersPerDay(ctx, db.GetNewUsersPerDayParams{
		CreatedAt:   startTs,
		CreatedAt_2: endTs,
	})
	if err != nil {
		return nil, err
	}

	newUsersOverTime := core.TimeSeries{Points: make([]core.TimeSeriesPoint, 0, len(newUsersData))}
	var totalNewUsers int64
	for _, row := range newUsersData {
		newUsersOverTime.Points = append(newUsersOverTime.Points, core.TimeSeriesPoint{
			Timestamp: int64(row.BucketTimestamp),
			Value:     row.UserCount,
		})
		totalNewUsers += row.UserCount
	}
	newUsersOverTime.Total = totalNewUsers

	// Get top users
	limit := int32(query.Limit)
	if limit <= 0 {
		limit = 10
	}
	topUsersData, err := s.queries.GetTopUsersByUsage(ctx, limit)
	if err != nil {
		s.log.Warn("failed to get top users", zap.Error(err))
	}

	topUsers := make([]core.TopUser, 0, len(topUsersData))
	for _, row := range topUsersData {
		topUsers = append(topUsers, core.TopUser{
			Pubkey:     row.Pubkey,
			UsedBytes:  row.UsedBytes,
			BlobCount:  row.BlobCount,
			LastActive: row.LastActive,
		})
	}

	// Get usage distribution
	distData, err := s.queries.GetUserUsageDistribution(ctx)
	if err != nil {
		s.log.Warn("failed to get usage distribution", zap.Error(err))
	}

	usersByUsage := make([]core.UsageBucket, 0, len(distData))
	var prevMax int64
	for _, row := range distData {
		maxBytes := int64(row.MaxBytes)
		usersByUsage = append(usersByUsage, core.UsageBucket{
			MinBytes:  prevMax,
			MaxBytes:  maxBytes,
			UserCount: row.UserCount,
		})
		prevMax = maxBytes
	}

	return &core.UserAnalytics{
		TotalUsers:       totalUsers,
		ActiveUsers:      activeUsers,
		NewUsersOverTime: newUsersOverTime,
		TopUsers:         topUsers,
		UsersByUsage:     usersByUsage,
	}, nil
}

func (s *analyticsService) GetContentAnalytics(ctx context.Context) (*core.ContentAnalytics, error) {
	// Get MIME type breakdown
	mimeData, err := s.queries.GetContentTypeBreakdown(ctx, 20)
	if err != nil {
		return nil, err
	}

	byMimeType := make([]core.MimeTypeBreakdown, 0, len(mimeData))
	for _, row := range mimeData {
		byMimeType = append(byMimeType, core.MimeTypeBreakdown{
			MimeType:  row.MimeType,
			BlobCount: row.BlobCount,
			TotalSize: toInt64(row.TotalSize),
		})
	}

	// Get category breakdown
	categoryData, err := s.queries.GetCategoryBreakdown(ctx)
	if err != nil {
		s.log.Warn("failed to get category breakdown", zap.Error(err))
	}

	byCategory := make([]core.CategoryBreakdown, 0, len(categoryData))
	for _, row := range categoryData {
		byCategory = append(byCategory, core.CategoryBreakdown{
			Category:  row.Category,
			BlobCount: row.BlobCount,
			TotalSize: toInt64(row.TotalSize),
		})
	}

	// Get encryption stats
	encStats, err := s.queries.GetEncryptionStats(ctx)
	if err != nil {
		s.log.Warn("failed to get encryption stats", zap.Error(err))
	}

	var encryptionPct float64
	if encStats.TotalBlobs > 0 {
		encrypted := encStats.ServerEncrypted + encStats.E2eEncrypted
		encryptionPct = float64(encrypted) / float64(encStats.TotalBlobs) * 100
	}

	return &core.ContentAnalytics{
		TotalTypes:    int64(len(mimeData)),
		ByMimeType:    byMimeType,
		ByCategory:    byCategory,
		EncryptionPct: encryptionPct,
	}, nil
}

// calculateGrowthPct calculates percentage change from previous to current value.
func calculateGrowthPct(previous, current int64) float64 {
	if previous == 0 {
		if current > 0 {
			return 100.0 // Infinite growth, cap at 100%
		}
		return 0
	}
	return float64(current-previous) / float64(previous) * 100
}
