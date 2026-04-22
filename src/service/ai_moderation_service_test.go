package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// mockCache implements cache.Cache for testing
type mockCache struct {
	data map[string][]byte
}

func newMockCache() *mockCache {
	return &mockCache{
		data: make(map[string][]byte),
	}
}

func (m *mockCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if data, exists := m.data[key]; exists {
		return data, true
	}
	return nil, false
}

func (m *mockCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockCache) Close() error {
	return nil
}

// mockModerationService implements core.ModerationService for testing
type mockModerationService struct {
	removedHashes map[string]string // hash -> reason
	reports       []*core.Report
	blocklist     map[string]*core.BlocklistEntry
}

func newMockModerationService() *mockModerationService {
	return &mockModerationService{
		removedHashes: make(map[string]string),
		reports:       make([]*core.Report, 0),
		blocklist:     make(map[string]*core.BlocklistEntry),
	}
}

// ReportService methods

func (m *mockModerationService) CreateReport(ctx context.Context, reporterPubkey, blobHash, blobURL string, reason core.ReportReason, details string) (*core.Report, error) {
	report := &core.Report{
		ID:             int32(len(m.reports) + 1),
		ReporterPubkey: reporterPubkey,
		BlobHash:       blobHash,
		BlobURL:        blobURL,
		Reason:         reason,
		Details:        details,
		Status:         core.ReportStatusPending,
		CreatedAt:      time.Now().Unix(),
	}
	m.reports = append(m.reports, report)
	return report, nil
}

func (m *mockModerationService) GetReport(ctx context.Context, id int32) (*core.Report, error) {
	for _, r := range m.reports {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, core.ErrReportNotFound
}

func (m *mockModerationService) ListPendingReports(ctx context.Context, limit, offset int) ([]*core.Report, error) {
	var pending []*core.Report
	for _, r := range m.reports {
		if r.Status == core.ReportStatusPending {
			pending = append(pending, r)
		}
	}
	return pending, nil
}

func (m *mockModerationService) ListAllReports(ctx context.Context, limit, offset int) ([]*core.Report, error) {
	return m.reports, nil
}

func (m *mockModerationService) ListReportsByStatus(ctx context.Context, status core.ReportStatus, limit, offset int) ([]*core.Report, error) {
	var filtered []*core.Report
	for _, r := range m.reports {
		if r.Status == status {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

func (m *mockModerationService) GetReportCountForBlob(ctx context.Context, blobHash string) (int64, error) {
	var count int64
	for _, r := range m.reports {
		if r.BlobHash == blobHash {
			count++
		}
	}
	return count, nil
}

func (m *mockModerationService) GetPendingReportCount(ctx context.Context) (int64, error) {
	var count int64
	for _, r := range m.reports {
		if r.Status == core.ReportStatusPending {
			count++
		}
	}
	return count, nil
}

func (m *mockModerationService) ReviewReport(ctx context.Context, id int32, status core.ReportStatus, action core.ReportAction, reviewerPubkey string) error {
	for _, r := range m.reports {
		if r.ID == id {
			r.Status = status
			r.ActionTaken = action
			r.ReviewedBy = reviewerPubkey
			r.ReviewedAt = time.Now().Unix()
			return nil
		}
	}
	return core.ErrReportNotFound
}

func (m *mockModerationService) GetTransparencyStats(ctx context.Context) (*core.TransparencyStats, error) {
	return &core.TransparencyStats{}, nil
}

// BlocklistService methods

func (m *mockModerationService) IsBlocked(ctx context.Context, pubkey string) (bool, error) {
	_, exists := m.blocklist[pubkey]
	return exists, nil
}

func (m *mockModerationService) AddToBlocklist(ctx context.Context, pubkey, reason, blockedBy string) (*core.BlocklistEntry, error) {
	entry := &core.BlocklistEntry{
		Pubkey:    pubkey,
		Reason:    reason,
		BlockedBy: blockedBy,
		CreatedAt: time.Now().Unix(),
	}
	m.blocklist[pubkey] = entry
	return entry, nil
}

func (m *mockModerationService) RemoveFromBlocklist(ctx context.Context, pubkey string) error {
	delete(m.blocklist, pubkey)
	return nil
}

func (m *mockModerationService) GetBlocklistEntry(ctx context.Context, pubkey string) (*core.BlocklistEntry, error) {
	if entry, exists := m.blocklist[pubkey]; exists {
		return entry, nil
	}
	return nil, nil
}

func (m *mockModerationService) ListBlocklist(ctx context.Context, limit, offset int) ([]*core.BlocklistEntry, error) {
	var entries []*core.BlocklistEntry
	for _, e := range m.blocklist {
		entries = append(entries, e)
	}
	return entries, nil
}

func (m *mockModerationService) GetBlocklistCount(ctx context.Context) (int64, error) {
	return int64(len(m.blocklist)), nil
}

// ModerationService specific methods

func (m *mockModerationService) ActionReport(ctx context.Context, reportID int32, action core.ReportAction, reviewerPubkey string) error {
	return m.ReviewReport(ctx, reportID, core.ReportStatusActioned, action, reviewerPubkey)
}

func (m *mockModerationService) IsHashRemoved(ctx context.Context, hash string) (bool, error) {
	_, exists := m.removedHashes[hash]
	return exists, nil
}

func (m *mockModerationService) AddRemovedBlob(ctx context.Context, hash, reason, removedBy string, reportID int32) error {
	m.removedHashes[hash] = reason
	return nil
}

func (m *mockModerationService) GetRemovedBlob(ctx context.Context, hash string) (*core.RemovedBlob, error) {
	if reason, exists := m.removedHashes[hash]; exists {
		return &core.RemovedBlob{
			Hash:      hash,
			Reason:    reason,
			RemovedBy: "system",
			CreatedAt: time.Now().Unix(),
		}, nil
	}
	return nil, nil
}

// mockAIProvider implements core.AIContentProvider for testing
type mockAIProvider struct {
	name           string
	mimeTypes      []string
	categories     []core.ContentCategory
	scanResult     *core.ScanResult
	scanError      error
	available      bool
	scanCallCount  int
}

func newMockAIProvider(name string, available bool) *mockAIProvider {
	return &mockAIProvider{
		name:       name,
		mimeTypes:  []string{"image/", "video/"},
		categories: []core.ContentCategory{core.CategoryCSAM, core.CategoryExplicitAdult},
		available:  available,
	}
}

func (m *mockAIProvider) Name() string {
	return m.name
}

func (m *mockAIProvider) SupportedMimeTypes() []string {
	return m.mimeTypes
}

func (m *mockAIProvider) SupportedCategories() []core.ContentCategory {
	return m.categories
}

func (m *mockAIProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	m.scanCallCount++
	if m.scanError != nil {
		return nil, m.scanError
	}
	if m.scanResult != nil {
		return m.scanResult, nil
	}
	// Default: allow
	return &core.ScanResult{
		Hash:              req.Hash,
		Provider:          m.name,
		Detections:        []core.ContentDetection{},
		RecommendedAction: core.ScanActionAllow,
		Confidence:        core.ConfidenceVeryLow,
		ScanDuration:      10 * time.Millisecond,
		ScannedAt:         time.Now().Unix(),
	}, nil
}

func (m *mockAIProvider) IsAvailable(ctx context.Context) bool {
	return m.available
}

// setupAIModerationTest creates a test environment
func setupAIModerationTest(t *testing.T) (*aiModerationService, *mockCache, *mockModerationService) {
	config := core.DefaultAIModerationConfig()
	config.Enabled = true
	config.CacheResults = true
	config.CacheTTL = 1 * time.Hour

	appCache := newMockCache()
	modService := newMockModerationService()
	log, _ := zap.NewDevelopment()

	svc := &aiModerationService{
		config:     config,
		providers:  make([]core.AIContentProvider, 0),
		cache:      appCache,
		moderation: modService,
		log:        log,
		scanQueue:  make(chan *core.ScanQueueItem, 1000),
		stopCh:     make(chan struct{}),
		stats: &aiStats{
			providerStats: make(map[string]int64),
			categoryStats: make(map[string]int64),
		},
	}

	return svc, appCache, modService
}

// TestNewAIModerationService tests service creation
func TestNewAIModerationService(t *testing.T) {
	config := core.DefaultAIModerationConfig()
	appCache := newMockCache()
	modService := newMockModerationService()
	log, _ := zap.NewDevelopment()

	svc, err := NewAIModerationService(config, appCache, modService, log)
	require.NoError(t, err)
	require.NotNil(t, svc)

	assert.False(t, svc.IsEnabled()) // Default config has enabled=false
}

// TestIsEnabled tests the IsEnabled method
func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name            string
		enabled         bool
		hasProviders    bool
		expectedEnabled bool
	}{
		{"enabled with providers", true, true, true},
		{"enabled without providers", true, false, false},
		{"disabled with providers", false, true, false},
		{"disabled without providers", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := setupAIModerationTest(t)
			svc.config.Enabled = tt.enabled
			if tt.hasProviders {
				svc.RegisterProvider(newMockAIProvider("test", true))
			}

			assert.Equal(t, tt.expectedEnabled, svc.IsEnabled())
		})
	}
}

// TestRegisterProvider tests provider registration
func TestRegisterProvider(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)

	provider1 := newMockAIProvider("provider1", true)
	provider2 := newMockAIProvider("provider2", true)

	svc.RegisterProvider(provider1)
	assert.Len(t, svc.GetProviders(), 1)

	svc.RegisterProvider(provider2)
	assert.Len(t, svc.GetProviders(), 2)

	providers := svc.GetProviders()
	assert.Equal(t, "provider1", providers[0].Name())
	assert.Equal(t, "provider2", providers[1].Name())
}

// TestShouldScan tests the ShouldScan method
func TestShouldScan(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	svc.config.SupportedMimeTypes = []string{"image/jpeg", "image/png", "video/"}
	svc.config.MaxScanSizeBytes = 100 * 1024 * 1024 // 100 MB

	tests := []struct {
		name         string
		mimeType     string
		size         int64
		shouldScan   bool
	}{
		{"supported image/jpeg", "image/jpeg", 1024, true},
		{"supported image/png", "image/png", 1024, true},
		{"supported video prefix", "video/mp4", 1024, true},
		{"unsupported text/plain", "text/plain", 1024, false},
		{"too large", "image/jpeg", 200 * 1024 * 1024, false},
		{"zero size", "image/jpeg", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.ShouldScan(tt.mimeType, tt.size)
			assert.Equal(t, tt.shouldScan, result)
		})
	}
}

// TestShouldScan_Disabled tests ShouldScan when AI moderation is disabled
func TestShouldScan_Disabled(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	svc.config.Enabled = false

	result := svc.ShouldScan("image/jpeg", 1024)
	assert.False(t, result)
}

// TestScanContent_Allow tests scanning content that should be allowed
func TestScanContent_Allow(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	provider.scanResult = &core.ScanResult{
		Hash:              "testhash",
		Provider:          "test_provider",
		Detections:        []core.ContentDetection{},
		RecommendedAction: core.ScanActionAllow,
		Confidence:        core.ConfidenceVeryLow,
		ScanDuration:      10 * time.Millisecond,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "testhash", result.Hash)
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Equal(t, 1, provider.scanCallCount)
}

// TestScanContent_Block tests scanning content that should be blocked
func TestScanContent_Block(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	provider.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "test_provider",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryCSAM,
				Confidence:  95,
				Description: "CSAM detected",
			},
		},
		RecommendedAction: core.ScanActionBlock,
		Confidence:        core.ConfidenceVeryHigh,
		ScanDuration:      10 * time.Millisecond,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
	assert.Len(t, result.Detections, 1)
	assert.Equal(t, core.CategoryCSAM, result.Detections[0].Category)
}

// TestScanContent_Quarantine tests scanning content that should be quarantined
func TestScanContent_Quarantine(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	provider.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "test_provider",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryCSAM,
				Confidence:  65,
				Description: "Possible CSAM",
			},
		},
		RecommendedAction: core.ScanActionQuarantine,
		Confidence:        core.ConfidenceMedium,
		ScanDuration:      10 * time.Millisecond,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionQuarantine, result.RecommendedAction)
}

// TestScanContent_Flag tests scanning content that should be flagged
func TestScanContent_Flag(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	provider.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "test_provider",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryExplicitAdult,
				Confidence:  30,
				Description: "Possibly explicit",
			},
		},
		RecommendedAction: core.ScanActionFlag,
		Confidence:        core.ConfidenceLow,
		ScanDuration:      10 * time.Millisecond,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionFlag, result.RecommendedAction)
}

// TestScanContent_CacheHit tests that cached results are returned
func TestScanContent_CacheHit(t *testing.T) {
	svc, appCache, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	svc.RegisterProvider(provider)

	// Pre-populate cache
	cachedResult := &core.ScanResult{
		Hash:              "cachedhash",
		Provider:          "cached",
		Detections:        []core.ContentDetection{},
		RecommendedAction: core.ScanActionAllow,
		Confidence:        core.ConfidenceVeryLow,
		ScannedAt:         time.Now().Unix(),
	}
	data, _ := json.Marshal(cachedResult)
	appCache.Set(ctx, "ai_scan:cachedhash", data, 1*time.Hour)

	req := &core.ScanRequest{
		Hash:     "cachedhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return cached result
	assert.Equal(t, "cached", result.Provider)
	// Provider should not be called
	assert.Equal(t, 0, provider.scanCallCount)
}

// TestScanContent_BlocklistCheck tests that blocklisted hashes are immediately blocked
func TestScanContent_BlocklistCheck(t *testing.T) {
	svc, _, modService := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	svc.RegisterProvider(provider)

	// Add hash to blocklist
	modService.removedHashes["blockedhash"] = "Previously removed content"

	req := &core.ScanRequest{
		Hash:     "blockedhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
	assert.Equal(t, "blocklist", result.Provider)
	assert.Len(t, result.Detections, 1)
	assert.Equal(t, core.CategoryCSAM, result.Detections[0].Category)
	// Provider should not be called
	assert.Equal(t, 0, provider.scanCallCount)
}

// TestScanContent_Disabled tests scanning when AI moderation is disabled
func TestScanContent_Disabled(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	svc.config.Enabled = false
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Equal(t, "disabled", result.Provider)
}

// TestScanContent_MultipleProviders tests aggregation of multiple provider results
func TestScanContent_MultipleProviders(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Provider 1: Low confidence detection
	provider1 := newMockAIProvider("provider1", true)
	provider1.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "provider1",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryExplicitAdult,
				Confidence:  40,
				Description: "Possibly explicit",
			},
		},
		RecommendedAction: core.ScanActionFlag,
		Confidence:        core.ConfidenceLow,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider1)

	// Provider 2: Different detection
	provider2 := newMockAIProvider("provider2", true)
	provider2.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "provider2",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryViolence,
				Confidence:  55,
				Description: "Violent content",
			},
		},
		RecommendedAction: core.ScanActionQuarantine,
		Confidence:        core.ConfidenceMedium,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider2)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should aggregate both results
	assert.Len(t, result.Detections, 2)
	assert.Contains(t, result.Provider, "provider1")
	assert.Contains(t, result.Provider, "provider2")

	// Both providers should be called
	assert.Equal(t, 1, provider1.scanCallCount)
	assert.Equal(t, 1, provider2.scanCallCount)
}

// TestScanContent_ProviderUnavailable tests handling when provider is unavailable
func TestScanContent_ProviderUnavailable(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Unavailable provider
	provider := newMockAIProvider("unavailable", false)
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should allow when no providers available
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Equal(t, "none", result.Provider)
}

// TestScanContent_CriticalCategoryEarlyReturn tests early return for critical categories
func TestScanContent_CriticalCategoryEarlyReturn(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Provider 1: Critical detection
	provider1 := newMockAIProvider("provider1", true)
	provider1.scanResult = &core.ScanResult{
		Hash:     "testhash",
		Provider: "provider1",
		Detections: []core.ContentDetection{
			{
				Category:    core.CategoryCSAM,
				Confidence:  90,
				Description: "CSAM detected",
			},
		},
		RecommendedAction: core.ScanActionBlock,
		Confidence:        core.ConfidenceVeryHigh,
		ScannedAt:         time.Now().Unix(),
	}
	svc.RegisterProvider(provider1)

	// Provider 2: Should not be called
	provider2 := newMockAIProvider("provider2", true)
	svc.RegisterProvider(provider2)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
	// Only first provider should be called
	assert.Equal(t, 1, provider1.scanCallCount)
	assert.Equal(t, 0, provider2.scanCallCount)
}

// TestDetermineActionFromDetections tests action determination logic
func TestDetermineActionFromDetections(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	svc.config.BlockHighConfidence = true
	svc.config.QuarantineMedium = true
	svc.config.FlagLowConfidence = true

	tests := []struct {
		name           string
		detections     []core.ContentDetection
		expectedAction core.ScanAction
	}{
		{
			name:           "no detections",
			detections:     []core.ContentDetection{},
			expectedAction: core.ScanActionAllow,
		},
		{
			name: "CSAM high confidence",
			detections: []core.ContentDetection{
				{Category: core.CategoryCSAM, Confidence: 95},
			},
			expectedAction: core.ScanActionBlock,
		},
		{
			name: "CSAM medium confidence",
			detections: []core.ContentDetection{
				{Category: core.CategoryCSAM, Confidence: 65},
			},
			expectedAction: core.ScanActionQuarantine,
		},
		{
			name: "explicit adult high confidence",
			detections: []core.ContentDetection{
				{Category: core.CategoryExplicitAdult, Confidence: 85},
			},
			expectedAction: core.ScanActionQuarantine,
		},
		{
			name: "violence medium confidence",
			detections: []core.ContentDetection{
				{Category: core.CategoryViolence, Confidence: 55},
			},
			expectedAction: core.ScanActionQuarantine,
		},
		{
			name: "spam low confidence",
			detections: []core.ContentDetection{
				{Category: core.CategorySpam, Confidence: 25},
			},
			expectedAction: core.ScanActionFlag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := svc.determineActionFromDetections(tt.detections)
			assert.Equal(t, tt.expectedAction, action)
		})
	}
}

// TestScanContentAsync tests async scanning
func TestScanContentAsync(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "asynchash",
		MimeType: "image/jpeg",
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	queueID, err := svc.ScanContentAsync(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, queueID)

	// Verify item is in queue
	item, err := svc.GetQueueItem(ctx, queueID)
	require.NoError(t, err)
	assert.Equal(t, "asynchash", item.Hash)
	assert.Equal(t, "testpubkey", item.Pubkey)
}

// TestGetScanResult tests retrieving cached scan results
func TestGetScanResult(t *testing.T) {
	svc, appCache, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Set up cached result
	result := &core.ScanResult{
		Hash:              "resulthash",
		Provider:          "test",
		RecommendedAction: core.ScanActionAllow,
		ScannedAt:         time.Now().Unix(),
	}
	data, _ := json.Marshal(result)
	appCache.Set(ctx, "ai_scan:resulthash", data, 1*time.Hour)

	retrieved, err := svc.GetScanResult(ctx, "resulthash")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "resulthash", retrieved.Hash)
}

// TestGetScanResult_NotFound tests retrieving non-existent result
func TestGetScanResult_NotFound(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	result, err := svc.GetScanResult(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestQuarantineBlob tests quarantining a blob
func TestQuarantineBlob(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	scanResult := &core.ScanResult{
		Hash: "quarantinehash",
		Detections: []core.ContentDetection{
			{Category: core.CategoryCSAM, Confidence: 65},
		},
		RecommendedAction: core.ScanActionQuarantine,
	}

	err := svc.QuarantineBlob(ctx, "quarantinehash", "testpubkey", scanResult)
	require.NoError(t, err)

	// Verify blob is quarantined
	qb, err := svc.GetQuarantinedBlob(ctx, "quarantinehash")
	require.NoError(t, err)
	assert.Equal(t, "quarantinehash", qb.Hash)
	assert.Equal(t, "testpubkey", qb.Pubkey)
	assert.Equal(t, "pending", qb.Status)
}

// TestListQuarantinedBlobs tests listing quarantined blobs
func TestListQuarantinedBlobs(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Add multiple quarantined blobs
	for i := 0; i < 5; i++ {
		hash := string(rune('a' + i)) + "hash"
		status := "pending"
		if i%2 == 0 {
			status = "approved"
		}

		qb := &core.QuarantinedBlob{
			Hash:      hash,
			Pubkey:    "testpubkey",
			Status:    status,
			CreatedAt: time.Now().Unix(),
		}
		svc.quarantine.Store(hash, qb)
	}

	// List all pending
	pending, err := svc.ListQuarantinedBlobs(ctx, "pending", 10, 0)
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	// List all approved
	approved, err := svc.ListQuarantinedBlobs(ctx, "approved", 10, 0)
	require.NoError(t, err)
	assert.Len(t, approved, 3)

	// List with offset
	allBlobs, err := svc.ListQuarantinedBlobs(ctx, "", 2, 1)
	require.NoError(t, err)
	assert.Len(t, allBlobs, 2)
}

// TestReviewQuarantinedBlob_Approve tests approving a quarantined blob
func TestReviewQuarantinedBlob_Approve(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Quarantine a blob
	qb := &core.QuarantinedBlob{
		Hash:      "reviewhash",
		Pubkey:    "testpubkey",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}
	svc.quarantine.Store("reviewhash", qb)

	// Approve
	err := svc.ReviewQuarantinedBlob(ctx, "reviewhash", true, "reviewerpubkey")
	require.NoError(t, err)

	// Check status
	reviewed, err := svc.GetQuarantinedBlob(ctx, "reviewhash")
	require.NoError(t, err)
	assert.Equal(t, "approved", reviewed.Status)
	assert.Equal(t, "reviewerpubkey", reviewed.ReviewedBy)
	assert.Greater(t, reviewed.ReviewedAt, int64(0))
}

// TestReviewQuarantinedBlob_Reject tests rejecting a quarantined blob
func TestReviewQuarantinedBlob_Reject(t *testing.T) {
	svc, _, modService := setupAIModerationTest(t)
	ctx := context.Background()

	// Quarantine a blob
	scanResult := &core.ScanResult{
		Hash: "rejecthash",
		Detections: []core.ContentDetection{
			{Category: core.CategoryCSAM, Confidence: 70},
		},
	}
	qb := &core.QuarantinedBlob{
		Hash:       "rejecthash",
		Pubkey:     "testpubkey",
		Status:     "pending",
		ScanResult: scanResult,
		CreatedAt:  time.Now().Unix(),
	}
	svc.quarantine.Store("rejecthash", qb)

	// Reject
	err := svc.ReviewQuarantinedBlob(ctx, "rejecthash", false, "reviewerpubkey")
	require.NoError(t, err)

	// Check status
	reviewed, err := svc.GetQuarantinedBlob(ctx, "rejecthash")
	require.NoError(t, err)
	assert.Equal(t, "rejected", reviewed.Status)

	// Verify added to removed list
	isRemoved, _ := modService.IsHashRemoved(ctx, "rejecthash")
	assert.True(t, isRemoved)
}

// TestGetQueueSize tests getting queue size
func TestGetQueueSize(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Initial size
	size, err := svc.GetQueueSize(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), size)

	// Queue some items
	for i := 0; i < 3; i++ {
		req := &core.ScanRequest{
			Hash:     "hash" + string(rune('0'+i)),
			MimeType: "image/jpeg",
			Size:     1024,
		}
		_, _ = svc.ScanContentAsync(ctx, req)
	}

	size, err = svc.GetQueueSize(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), size)
}

// TestGetStats tests getting AI moderation statistics
func TestGetStats(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	// Update some stats
	svc.statsMu.Lock()
	svc.stats.totalScans = 100
	svc.stats.blockedCount = 5
	svc.stats.quarantinedCount = 10
	svc.stats.flaggedCount = 15
	svc.stats.allowedCount = 70
	svc.stats.totalScanTime = 1 * time.Second
	svc.stats.providerStats["provider1"] = 50
	svc.stats.providerStats["provider2"] = 50
	svc.stats.categoryStats["csam"] = 5
	svc.stats.categoryStats["explicit_adult"] = 10
	svc.statsMu.Unlock()

	stats, err := svc.GetStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, int64(100), stats.TotalScans)
	assert.Equal(t, int64(5), stats.BlockedCount)
	assert.Equal(t, int64(10), stats.QuarantinedCount)
	assert.Equal(t, int64(15), stats.FlaggedCount)
	assert.Equal(t, int64(70), stats.AllowedCount)
	assert.Equal(t, 10*time.Millisecond, stats.AvgScanTime)
	assert.Len(t, stats.ProviderStats, 2)
	assert.Len(t, stats.CategoryStats, 2)
}

// TestUpdateStats tests stats updating
func TestUpdateStats(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)

	result := &core.ScanResult{
		Hash: "testhash",
		Detections: []core.ContentDetection{
			{Category: core.CategoryCSAM, Confidence: 95},
			{Category: core.CategoryExplicitAdult, Confidence: 80},
		},
		RecommendedAction: core.ScanActionBlock,
		ScanDuration:      100 * time.Millisecond,
	}

	svc.updateStats(result)

	svc.statsMu.RLock()
	defer svc.statsMu.RUnlock()

	assert.Equal(t, int64(1), svc.stats.totalScans)
	assert.Equal(t, int64(1), svc.stats.blockedCount)
	assert.Equal(t, 100*time.Millisecond, svc.stats.totalScanTime)
	assert.Equal(t, int64(1), svc.stats.categoryStats["csam"])
	assert.Equal(t, int64(1), svc.stats.categoryStats["explicit_adult"])
}

// TestProviderSupportsType tests MIME type checking for providers
func TestProviderSupportsType(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)

	provider := newMockAIProvider("test", true)
	provider.mimeTypes = []string{"image/jpeg", "image/png", "video/"}

	tests := []struct {
		mimeType  string
		supported bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"video/mp4", true},
		{"video/webm", true},
		{"text/plain", false},
		{"application/json", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			result := svc.providerSupportsType(provider, tt.mimeType)
			assert.Equal(t, tt.supported, result)
		})
	}
}

// TestScanContent_UnsupportedMimeType tests scanning with unsupported MIME type
func TestScanContent_UnsupportedMimeType(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)
	ctx := context.Background()

	provider := newMockAIProvider("test_provider", true)
	provider.mimeTypes = []string{"image/jpeg", "image/png"}
	svc.RegisterProvider(provider)

	req := &core.ScanRequest{
		Hash:     "testhash",
		MimeType: "text/plain", // Unsupported
		Size:     1024,
		Pubkey:   "testpubkey",
	}

	result, err := svc.ScanContent(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return allow when no providers support the type
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Equal(t, "none", result.Provider)
	// Provider should not be called
	assert.Equal(t, 0, provider.scanCallCount)
}

// TestAggregateResults tests result aggregation logic
func TestAggregateResults(t *testing.T) {
	svc, _, _ := setupAIModerationTest(t)

	tests := []struct {
		name              string
		results           []*core.ScanResult
		expectedProvider  string
		expectedAction    core.ScanAction
		expectedDetCount  int
	}{
		{
			name:             "no results",
			results:          []*core.ScanResult{},
			expectedProvider: "none",
			expectedAction:   core.ScanActionAllow,
			expectedDetCount: 0,
		},
		{
			name: "single result",
			results: []*core.ScanResult{
				{
					Provider: "provider1",
					Detections: []core.ContentDetection{
						{Category: core.CategoryExplicitAdult, Confidence: 60},
					},
				},
			},
			expectedProvider: "provider1",
			expectedDetCount: 1,
		},
		{
			name: "multiple results",
			results: []*core.ScanResult{
				{
					Provider: "provider1",
					Detections: []core.ContentDetection{
						{Category: core.CategoryExplicitAdult, Confidence: 60},
					},
				},
				{
					Provider: "provider2",
					Detections: []core.ContentDetection{
						{Category: core.CategoryViolence, Confidence: 70},
					},
				},
			},
			expectedProvider: "provider1,provider2",
			expectedDetCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.aggregateResults("testhash", tt.results, 10*time.Millisecond)
			assert.Contains(t, result.Provider, tt.expectedProvider)
			assert.Len(t, result.Detections, tt.expectedDetCount)
		})
	}
}
