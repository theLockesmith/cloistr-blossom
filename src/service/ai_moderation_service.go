package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type aiModerationService struct {
	config      core.AIModerationConfig
	providers   []core.AIContentProvider
	cache       cache.Cache
	moderation  core.ModerationService
	log         *zap.Logger

	// Quarantine storage (in production, use database)
	quarantine  sync.Map // hash -> *core.QuarantinedBlob

	// Scan queue for async processing
	scanQueue   chan *core.ScanQueueItem
	queueItems  sync.Map // id -> *core.ScanQueueItem

	// Statistics
	stats       *aiStats
	statsMu     sync.RWMutex

	// Worker management
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

type aiStats struct {
	totalScans       int64
	blockedCount     int64
	quarantinedCount int64
	flaggedCount     int64
	allowedCount     int64
	totalScanTime    time.Duration
	providerStats    map[string]int64
	categoryStats    map[string]int64
}

// NewAIModerationService creates a new AI moderation service.
func NewAIModerationService(
	config core.AIModerationConfig,
	appCache cache.Cache,
	moderation core.ModerationService,
	log *zap.Logger,
) (core.AIModerationService, error) {
	svc := &aiModerationService{
		config:     config,
		providers:  make([]core.AIContentProvider, 0),
		cache:      appCache,
		moderation: moderation,
		log:        log,
		scanQueue:  make(chan *core.ScanQueueItem, 1000),
		stopCh:     make(chan struct{}),
		stats: &aiStats{
			providerStats: make(map[string]int64),
			categoryStats: make(map[string]int64),
		},
	}

	return svc, nil
}

// RegisterProvider adds a content analysis provider.
func (s *aiModerationService) RegisterProvider(provider core.AIContentProvider) {
	s.providers = append(s.providers, provider)
	s.log.Info("registered AI content provider",
		zap.String("name", provider.Name()),
		zap.Strings("mime_types", provider.SupportedMimeTypes()))
}

// GetProviders returns registered providers.
func (s *aiModerationService) GetProviders() []core.AIContentProvider {
	return s.providers
}

// IsEnabled returns whether AI moderation is enabled.
func (s *aiModerationService) IsEnabled() bool {
	return s.config.Enabled && len(s.providers) > 0
}

// ShouldScan determines if content should be scanned based on config.
func (s *aiModerationService) ShouldScan(mimeType string, size int64) bool {
	if !s.config.Enabled {
		return false
	}

	// Check size limit
	if s.config.MaxScanSizeBytes > 0 && size > s.config.MaxScanSizeBytes {
		return false
	}

	// Check if MIME type is supported
	for _, supported := range s.config.SupportedMimeTypes {
		if strings.HasPrefix(mimeType, supported) || mimeType == supported {
			return true
		}
	}

	return false
}

// ScanContent scans content for harmful material.
func (s *aiModerationService) ScanContent(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	if !s.config.Enabled {
		return &core.ScanResult{
			Hash:              req.Hash,
			Provider:          "disabled",
			RecommendedAction: core.ScanActionAllow,
			ScannedAt:         time.Now().Unix(),
		}, nil
	}

	// Check cache first
	if s.config.CacheResults {
		if cached, err := s.GetScanResult(ctx, req.Hash); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Check if hash is in removed list (quick check)
	if s.moderation != nil {
		isRemoved, _ := s.moderation.IsHashRemoved(ctx, req.Hash)
		if isRemoved {
			return &core.ScanResult{
				Hash:     req.Hash,
				Provider: "blocklist",
				Detections: []core.ContentDetection{
					{
						Category:    core.CategoryCSAM,
						Confidence:  100,
						Description: "Hash matches previously removed content",
					},
				},
				RecommendedAction: core.ScanActionBlock,
				Confidence:        core.ConfidenceVeryHigh,
				ScannedAt:         time.Now().Unix(),
			}, nil
		}
	}

	start := time.Now()

	// Find suitable providers for this content type
	var results []*core.ScanResult
	for _, provider := range s.providers {
		if !s.providerSupportsType(provider, req.MimeType) {
			continue
		}

		if !provider.IsAvailable(ctx) {
			s.log.Warn("provider not available", zap.String("provider", provider.Name()))
			continue
		}

		result, err := provider.Scan(ctx, req)
		if err != nil {
			s.log.Error("provider scan failed",
				zap.String("provider", provider.Name()),
				zap.Error(err))
			continue
		}

		results = append(results, result)

		// Update provider stats
		s.statsMu.Lock()
		s.stats.providerStats[provider.Name()]++
		s.statsMu.Unlock()

		// For critical categories, one detection is enough
		for _, det := range result.Detections {
			if core.IsCriticalCategory(det.Category) && det.Confidence >= 80 {
				result.RecommendedAction = core.ScanActionBlock
				s.cacheResult(ctx, result)
				return result, nil
			}
		}
	}

	// Aggregate results from all providers
	finalResult := s.aggregateResults(req.Hash, results, time.Since(start))

	// Update stats
	s.updateStats(finalResult)

	// Cache the result
	s.cacheResult(ctx, finalResult)

	return finalResult, nil
}

// aggregateResults combines results from multiple providers.
func (s *aiModerationService) aggregateResults(hash string, results []*core.ScanResult, duration time.Duration) *core.ScanResult {
	if len(results) == 0 {
		return &core.ScanResult{
			Hash:              hash,
			Provider:          "none",
			RecommendedAction: core.ScanActionAllow,
			Confidence:        core.ConfidenceVeryLow,
			ScanDuration:      duration,
			ScannedAt:         time.Now().Unix(),
		}
	}

	// Aggregate all detections
	var allDetections []core.ContentDetection
	var providers []string
	var maxConfidence float64

	for _, result := range results {
		providers = append(providers, result.Provider)
		allDetections = append(allDetections, result.Detections...)
		for _, det := range result.Detections {
			if det.Confidence > maxConfidence {
				maxConfidence = det.Confidence
			}
		}
	}

	// Sort detections by confidence (highest first)
	sort.Slice(allDetections, func(i, j int) bool {
		return allDetections[i].Confidence > allDetections[j].Confidence
	})

	// Determine recommended action
	action := s.determineActionFromDetections(allDetections)

	return &core.ScanResult{
		Hash:              hash,
		Provider:          strings.Join(providers, ","),
		Detections:        allDetections,
		RecommendedAction: action,
		Confidence:        core.ConfidenceToLevel(maxConfidence),
		ScanDuration:      duration,
		ScannedAt:         time.Now().Unix(),
	}
}

// determineActionFromDetections determines action based on detections.
func (s *aiModerationService) determineActionFromDetections(detections []core.ContentDetection) core.ScanAction {
	if len(detections) == 0 {
		return core.ScanActionAllow
	}

	for _, det := range detections {
		// Critical categories with high confidence = block
		if core.IsCriticalCategory(det.Category) && det.Confidence >= 80 {
			return core.ScanActionBlock
		}

		// Critical categories with medium confidence = quarantine
		if core.IsCriticalCategory(det.Category) && det.Confidence >= 50 {
			if s.config.QuarantineMedium {
				return core.ScanActionQuarantine
			}
		}

		// Non-critical high confidence
		if det.Confidence >= 80 && s.config.BlockHighConfidence {
			if det.Category == core.CategoryExplicitAdult || det.Category == core.CategoryViolence {
				return core.ScanActionQuarantine
			}
		}

		// Medium confidence = quarantine
		if det.Confidence >= 50 && s.config.QuarantineMedium {
			return core.ScanActionQuarantine
		}

		// Low confidence = flag
		if det.Confidence >= 20 && s.config.FlagLowConfidence {
			return core.ScanActionFlag
		}
	}

	return core.ScanActionAllow
}

// DetermineAction determines the action based on scan results and config.
func (s *aiModerationService) DetermineAction(result *core.ScanResult) core.ScanAction {
	return s.determineActionFromDetections(result.Detections)
}

// ScanContentAsync queues content for async scanning.
func (s *aiModerationService) ScanContentAsync(ctx context.Context, req *core.ScanRequest) (string, error) {
	item := &core.ScanQueueItem{
		ID:        uuid.New().String(),
		Hash:      req.Hash,
		Pubkey:    req.Pubkey,
		MimeType:  req.MimeType,
		Size:      req.Size,
		Priority:  10, // Default priority
		CreatedAt: time.Now().Unix(),
	}

	s.queueItems.Store(item.ID, item)

	select {
	case s.scanQueue <- item:
		s.log.Debug("queued content for async scan",
			zap.String("id", item.ID),
			zap.String("hash", item.Hash))
		return item.ID, nil
	default:
		return "", fmt.Errorf("scan queue is full")
	}
}

// GetScanResult retrieves a cached scan result.
func (s *aiModerationService) GetScanResult(ctx context.Context, hash string) (*core.ScanResult, error) {
	if s.cache == nil {
		return nil, fmt.Errorf("cache not available")
	}

	key := fmt.Sprintf("ai_scan:%s", hash)
	data, found := s.cache.Get(ctx, key)
	if !found || data == nil {
		return nil, nil
	}

	// Decode cached result
	var result core.ScanResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to decode cached result: %w", err)
	}

	return &result, nil
}

// cacheResult stores a scan result in cache.
func (s *aiModerationService) cacheResult(ctx context.Context, result *core.ScanResult) {
	if s.cache == nil || !s.config.CacheResults {
		return
	}

	data, err := json.Marshal(result)
	if err != nil {
		s.log.Warn("failed to marshal scan result for cache", zap.Error(err))
		return
	}

	key := fmt.Sprintf("ai_scan:%s", result.Hash)
	_ = s.cache.Set(ctx, key, data, s.config.CacheTTL)
}

// QuarantineBlob places a blob in quarantine pending review.
func (s *aiModerationService) QuarantineBlob(ctx context.Context, hash, pubkey string, scanResult *core.ScanResult) error {
	qb := &core.QuarantinedBlob{
		Hash:       hash,
		Pubkey:     pubkey,
		MimeType:   "",
		Size:       0,
		ScanResult: scanResult,
		Status:     "pending",
		CreatedAt:  time.Now().Unix(),
	}

	s.quarantine.Store(hash, qb)

	s.statsMu.Lock()
	s.stats.quarantinedCount++
	s.statsMu.Unlock()

	s.log.Info("blob quarantined",
		zap.String("hash", hash),
		zap.String("pubkey", pubkey))

	return nil
}

// GetQuarantinedBlob returns a quarantined blob by hash.
func (s *aiModerationService) GetQuarantinedBlob(ctx context.Context, hash string) (*core.QuarantinedBlob, error) {
	if val, ok := s.quarantine.Load(hash); ok {
		return val.(*core.QuarantinedBlob), nil
	}
	return nil, fmt.Errorf("quarantined blob not found: %s", hash)
}

// ListQuarantinedBlobs returns blobs pending review.
func (s *aiModerationService) ListQuarantinedBlobs(ctx context.Context, status string, limit, offset int) ([]*core.QuarantinedBlob, error) {
	var blobs []*core.QuarantinedBlob
	count := 0
	skipped := 0

	s.quarantine.Range(func(key, value interface{}) bool {
		qb := value.(*core.QuarantinedBlob)
		if status != "" && qb.Status != status {
			return true
		}

		if skipped < offset {
			skipped++
			return true
		}

		if count >= limit {
			return false
		}

		blobs = append(blobs, qb)
		count++
		return true
	})

	return blobs, nil
}

// ReviewQuarantinedBlob approves or rejects a quarantined blob.
func (s *aiModerationService) ReviewQuarantinedBlob(ctx context.Context, hash string, approved bool, reviewerPubkey string) error {
	val, ok := s.quarantine.Load(hash)
	if !ok {
		return fmt.Errorf("quarantined blob not found: %s", hash)
	}

	qb := val.(*core.QuarantinedBlob)
	qb.ReviewedBy = reviewerPubkey
	qb.ReviewedAt = time.Now().Unix()

	if approved {
		qb.Status = "approved"
		s.log.Info("quarantined blob approved",
			zap.String("hash", hash),
			zap.String("reviewer", reviewerPubkey))
	} else {
		qb.Status = "rejected"

		// Add to removed list if moderation service available
		if s.moderation != nil {
			reason := "AI moderation: rejected"
			if qb.ScanResult != nil && len(qb.ScanResult.Detections) > 0 {
				reason = fmt.Sprintf("AI moderation: %s (%.0f%% confidence)",
					qb.ScanResult.Detections[0].Category,
					qb.ScanResult.Detections[0].Confidence)
			}
			_ = s.moderation.AddRemovedBlob(ctx, hash, reason, reviewerPubkey, 0)
		}

		s.log.Info("quarantined blob rejected",
			zap.String("hash", hash),
			zap.String("reviewer", reviewerPubkey))
	}

	return nil
}

// GetQueueItem returns an item from the scan queue.
func (s *aiModerationService) GetQueueItem(ctx context.Context, id string) (*core.ScanQueueItem, error) {
	if val, ok := s.queueItems.Load(id); ok {
		return val.(*core.ScanQueueItem), nil
	}
	return nil, fmt.Errorf("queue item not found: %s", id)
}

// GetQueueSize returns the current queue size.
func (s *aiModerationService) GetQueueSize(ctx context.Context) (int64, error) {
	return int64(len(s.scanQueue)), nil
}

// GetStats returns AI moderation statistics.
func (s *aiModerationService) GetStats(ctx context.Context) (*core.AIScanStats, error) {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()

	queueSize, _ := s.GetQueueSize(ctx)

	var avgScanTime time.Duration
	if s.stats.totalScans > 0 {
		avgScanTime = s.stats.totalScanTime / time.Duration(s.stats.totalScans)
	}

	// Copy maps
	providerStats := make(map[string]int64)
	for k, v := range s.stats.providerStats {
		providerStats[k] = v
	}
	categoryStats := make(map[string]int64)
	for k, v := range s.stats.categoryStats {
		categoryStats[k] = v
	}

	return &core.AIScanStats{
		TotalScans:       s.stats.totalScans,
		BlockedCount:     s.stats.blockedCount,
		QuarantinedCount: s.stats.quarantinedCount,
		FlaggedCount:     s.stats.flaggedCount,
		AllowedCount:     s.stats.allowedCount,
		AvgScanTime:      avgScanTime,
		QueueSize:        queueSize,
		ProviderStats:    providerStats,
		CategoryStats:    categoryStats,
	}, nil
}

// updateStats updates statistics based on scan result.
func (s *aiModerationService) updateStats(result *core.ScanResult) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	s.stats.totalScans++
	s.stats.totalScanTime += result.ScanDuration

	switch result.RecommendedAction {
	case core.ScanActionBlock:
		s.stats.blockedCount++
	case core.ScanActionQuarantine:
		s.stats.quarantinedCount++
	case core.ScanActionFlag:
		s.stats.flaggedCount++
	case core.ScanActionAllow:
		s.stats.allowedCount++
	}

	for _, det := range result.Detections {
		s.stats.categoryStats[string(det.Category)]++
	}
}

// Start starts the async scan workers.
func (s *aiModerationService) Start(ctx context.Context) {
	if !s.config.Enabled {
		s.log.Info("AI moderation disabled")
		return
	}

	workerCount := s.config.WorkerCount
	if workerCount <= 0 {
		workerCount = 2
	}

	for i := 0; i < workerCount; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	s.log.Info("AI moderation service started",
		zap.Int("workers", workerCount),
		zap.Int("providers", len(s.providers)))
}

// worker processes async scan queue.
func (s *aiModerationService) worker(ctx context.Context, id int) {
	defer s.wg.Done()

	s.log.Debug("AI scan worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case item := <-s.scanQueue:
			s.processQueueItem(ctx, item)
		}
	}
}

// processQueueItem handles a single scan queue item.
func (s *aiModerationService) processQueueItem(ctx context.Context, item *core.ScanQueueItem) {
	// Note: In production, you'd need to retrieve the actual blob data
	// This is a simplified implementation
	req := &core.ScanRequest{
		Hash:     item.Hash,
		MimeType: item.MimeType,
		Size:     item.Size,
		Pubkey:   item.Pubkey,
	}

	result, err := s.ScanContent(ctx, req)
	if err != nil {
		item.Attempts++
		item.LastError = err.Error()

		if item.Attempts < s.config.RetryAttempts {
			// Requeue
			time.Sleep(s.config.RetryDelay)
			select {
			case s.scanQueue <- item:
			default:
				s.log.Warn("could not requeue failed scan", zap.String("id", item.ID))
			}
		}
		return
	}

	// Handle result
	switch result.RecommendedAction {
	case core.ScanActionBlock:
		if s.moderation != nil {
			reason := "AI moderation: auto-blocked"
			if len(result.Detections) > 0 {
				reason = fmt.Sprintf("AI moderation: %s (%.0f%%)",
					result.Detections[0].Category,
					result.Detections[0].Confidence)
			}
			_ = s.moderation.AddRemovedBlob(ctx, item.Hash, reason, "system", 0)
		}

	case core.ScanActionQuarantine:
		_ = s.QuarantineBlob(ctx, item.Hash, item.Pubkey, result)

	case core.ScanActionFlag:
		// Create a report for human review
		if s.moderation != nil {
			reason := core.ReportReasonOther
			if len(result.Detections) > 0 {
				reason = core.CategoryToReportReason(result.Detections[0].Category)
			}
			details := fmt.Sprintf("AI flagged: %v", result.Detections)
			_, _ = s.moderation.CreateReport(ctx, "system", item.Hash, "", reason, details)
		}
	}

	// Clean up queue item
	s.queueItems.Delete(item.ID)
}

// Stop stops the workers.
func (s *aiModerationService) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	s.log.Info("AI moderation service stopped")
}

// providerSupportsType checks if a provider supports a MIME type.
func (s *aiModerationService) providerSupportsType(provider core.AIContentProvider, mimeType string) bool {
	for _, supported := range provider.SupportedMimeTypes() {
		if strings.HasPrefix(mimeType, supported) || mimeType == supported {
			return true
		}
	}
	return false
}

// Ensure interface compliance
var _ core.AIModerationService = (*aiModerationService)(nil)
