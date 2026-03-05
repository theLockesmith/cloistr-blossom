package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// mockAIModerationService implements core.AIModerationService for testing
type mockAIModerationService struct {
	stats             *core.AIScanStats
	quarantinedBlobs  map[string]*core.QuarantinedBlob
	providers         []core.AIContentProvider
	scanResults       map[string]*core.ScanResult
	queueSize         int64
	enabled           bool
	scanAsyncQueueID  string
	scanAsyncError    error
}

func newMockAIModerationService() *mockAIModerationService {
	return &mockAIModerationService{
		stats: &core.AIScanStats{
			TotalScans:       100,
			BlockedCount:     5,
			QuarantinedCount: 10,
			FlaggedCount:     15,
			AllowedCount:     70,
			AvgScanTime:      100 * time.Millisecond,
			QueueSize:        3,
			ProviderStats: map[string]int64{
				"provider1": 50,
				"provider2": 50,
			},
			CategoryStats: map[string]int64{
				"csam":           5,
				"explicit_adult": 10,
			},
		},
		quarantinedBlobs: make(map[string]*core.QuarantinedBlob),
		providers:        make([]core.AIContentProvider, 0),
		scanResults:      make(map[string]*core.ScanResult),
		queueSize:        3,
		enabled:          true,
	}
}

func (m *mockAIModerationService) ScanContent(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	return nil, nil
}

func (m *mockAIModerationService) ScanContentAsync(ctx context.Context, req *core.ScanRequest) (string, error) {
	if m.scanAsyncError != nil {
		return "", m.scanAsyncError
	}
	return m.scanAsyncQueueID, nil
}

func (m *mockAIModerationService) GetScanResult(ctx context.Context, hash string) (*core.ScanResult, error) {
	if result, exists := m.scanResults[hash]; exists {
		return result, nil
	}
	return nil, nil
}

func (m *mockAIModerationService) QuarantineBlob(ctx context.Context, hash, pubkey string, scanResult *core.ScanResult) error {
	return nil
}

func (m *mockAIModerationService) GetQuarantinedBlob(ctx context.Context, hash string) (*core.QuarantinedBlob, error) {
	if blob, exists := m.quarantinedBlobs[hash]; exists {
		return blob, nil
	}
	return nil, core.ErrBlobNotFound
}

func (m *mockAIModerationService) ListQuarantinedBlobs(ctx context.Context, status string, limit, offset int) ([]*core.QuarantinedBlob, error) {
	var blobs []*core.QuarantinedBlob
	for _, blob := range m.quarantinedBlobs {
		if status == "" || blob.Status == status {
			blobs = append(blobs, blob)
		}
	}
	// Simple pagination
	if offset >= len(blobs) {
		return []*core.QuarantinedBlob{}, nil
	}
	end := offset + limit
	if end > len(blobs) {
		end = len(blobs)
	}
	return blobs[offset:end], nil
}

func (m *mockAIModerationService) ReviewQuarantinedBlob(ctx context.Context, hash string, approved bool, reviewerPubkey string) error {
	if blob, exists := m.quarantinedBlobs[hash]; exists {
		if approved {
			blob.Status = "approved"
		} else {
			blob.Status = "rejected"
		}
		blob.ReviewedBy = reviewerPubkey
		blob.ReviewedAt = time.Now().Unix()
		return nil
	}
	return core.ErrBlobNotFound
}

func (m *mockAIModerationService) GetQueueItem(ctx context.Context, id string) (*core.ScanQueueItem, error) {
	return nil, nil
}

func (m *mockAIModerationService) GetQueueSize(ctx context.Context) (int64, error) {
	return m.queueSize, nil
}

func (m *mockAIModerationService) RegisterProvider(provider core.AIContentProvider) {
	m.providers = append(m.providers, provider)
}

func (m *mockAIModerationService) GetProviders() []core.AIContentProvider {
	return m.providers
}

func (m *mockAIModerationService) ShouldScan(mimeType string, size int64) bool {
	return true
}

func (m *mockAIModerationService) DetermineAction(result *core.ScanResult) core.ScanAction {
	return result.RecommendedAction
}

func (m *mockAIModerationService) GetStats(ctx context.Context) (*core.AIScanStats, error) {
	return m.stats, nil
}

func (m *mockAIModerationService) Start(ctx context.Context) {}

func (m *mockAIModerationService) Stop() {}

func (m *mockAIModerationService) IsEnabled() bool {
	return m.enabled
}

// mockProvider implements core.AIContentProvider for testing
type mockProvider struct {
	name       string
	mimeTypes  []string
	categories []core.ContentCategory
	available  bool
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) SupportedMimeTypes() []string {
	return m.mimeTypes
}

func (m *mockProvider) SupportedCategories() []core.ContentCategory {
	return m.categories
}

func (m *mockProvider) Scan(ctx context.Context, req *core.ScanRequest) (*core.ScanResult, error) {
	return nil, nil
}

func (m *mockProvider) IsAvailable(ctx context.Context) bool {
	return m.available
}

// setupAIModerationRouter creates a test router with AI moderation controller
func setupAIModerationRouter(aiMod core.AIModerationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	controller := NewAIModerationController(aiMod)

	// Admin middleware stub (just sets pubkey)
	adminMiddleware := func(c *gin.Context) {
		c.Set("pubkey", "adminpubkey")
		c.Next()
	}

	RegisterAIModerationRoutes(r, controller, adminMiddleware)

	return r
}

// TestGetStats tests the GetStats endpoint
func TestGetStats(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/stats", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var stats core.AIScanStats
	err := json.Unmarshal(w.Body.Bytes(), &stats)
	require.NoError(t, err)

	assert.Equal(t, int64(100), stats.TotalScans)
	assert.Equal(t, int64(5), stats.BlockedCount)
	assert.Equal(t, int64(10), stats.QuarantinedCount)
	assert.Len(t, stats.ProviderStats, 2)
}

// TestGetStats_Disabled tests GetStats when AI moderation is disabled
func TestGetStats_Disabled(t *testing.T) {
	r := setupAIModerationRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/stats", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestListQuarantined tests the ListQuarantined endpoint
func TestListQuarantined(t *testing.T) {
	aiMod := newMockAIModerationService()

	// Add test quarantined blobs
	for i := 0; i < 5; i++ {
		status := "pending"
		if i%2 == 0 {
			status = "approved"
		}
		hash := "hash" + string(rune('0'+i))
		aiMod.quarantinedBlobs[hash] = &core.QuarantinedBlob{
			Hash:      hash,
			Pubkey:    "testpubkey",
			Status:    status,
			CreatedAt: time.Now().Unix(),
		}
	}

	r := setupAIModerationRouter(aiMod)

	tests := []struct {
		name           string
		queryParams    string
		expectedCount  int
		expectedStatus string
	}{
		{"all pending", "?status=pending", 2, "pending"},
		{"all approved", "?status=approved", 3, "approved"},
		{"default (pending)", "", 2, "pending"},
		{"with limit", "?limit=1", 1, ""},
		{"with offset", "?offset=1&limit=10&status=pending", 1, "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var response struct {
				Blobs  []*core.QuarantinedBlob `json:"blobs"`
				Count  int                     `json:"count"`
				Limit  int                     `json:"limit"`
				Offset int                     `json:"offset"`
			}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedCount, response.Count)
			if tt.expectedStatus != "" {
				for _, blob := range response.Blobs {
					assert.Equal(t, tt.expectedStatus, blob.Status)
				}
			}
		})
	}
}

// TestListQuarantined_InvalidParams tests invalid query parameters
func TestListQuarantined_InvalidParams(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	tests := []struct {
		name        string
		queryParams string
	}{
		{"invalid limit", "?limit=invalid"},
		{"negative offset", "?offset=-1"},
		{"limit too large", "?limit=1000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// Should still succeed with default values
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestGetQuarantinedBlob tests the GetQuarantinedBlob endpoint
func TestGetQuarantinedBlob(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.quarantinedBlobs["testhash"] = &core.QuarantinedBlob{
		Hash:      "testhash",
		Pubkey:    "testpubkey",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine/testhash", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var blob core.QuarantinedBlob
	err := json.Unmarshal(w.Body.Bytes(), &blob)
	require.NoError(t, err)

	assert.Equal(t, "testhash", blob.Hash)
	assert.Equal(t, "testpubkey", blob.Pubkey)
	assert.Equal(t, "pending", blob.Status)
}

// TestGetQuarantinedBlob_NotFound tests getting non-existent blob
func TestGetQuarantinedBlob_NotFound(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine/nonexistent", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGetQuarantinedBlob_EmptyHash tests with empty hash
func TestGetQuarantinedBlob_EmptyHash(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	// Note: With Gin routing, /quarantine/ redirects to /quarantine (list endpoint)
	// So requesting with trailing slash gives 301 redirect to list
	// This is expected Gin behavior - testing that trailing slash is handled
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMovedPermanently, w.Code) // Gin redirects trailing slash
}

// TestReviewQuarantinedBlob_Approve tests approving a quarantined blob
func TestReviewQuarantinedBlob_Approve(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.quarantinedBlobs["testhash"] = &core.QuarantinedBlob{
		Hash:      "testhash",
		Pubkey:    "testpubkey",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}

	r := setupAIModerationRouter(aiMod)

	body := map[string]bool{"approved": true}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/quarantine/testhash/review", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "approved", response["status"])
	assert.Equal(t, "testhash", response["hash"])

	// Verify blob status updated
	blob := aiMod.quarantinedBlobs["testhash"]
	assert.Equal(t, "approved", blob.Status)
	assert.Equal(t, "adminpubkey", blob.ReviewedBy)
}

// TestReviewQuarantinedBlob_Reject tests rejecting a quarantined blob
func TestReviewQuarantinedBlob_Reject(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.quarantinedBlobs["testhash"] = &core.QuarantinedBlob{
		Hash:      "testhash",
		Pubkey:    "testpubkey",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}

	r := setupAIModerationRouter(aiMod)

	body := map[string]bool{"approved": false}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/quarantine/testhash/review", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "rejected", response["status"])

	// Verify blob status updated
	blob := aiMod.quarantinedBlobs["testhash"]
	assert.Equal(t, "rejected", blob.Status)
}

// TestReviewQuarantinedBlob_InvalidJSON tests with invalid JSON
func TestReviewQuarantinedBlob_InvalidJSON(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/quarantine/testhash/review", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestReviewQuarantinedBlob_MissingHash tests with missing hash
func TestReviewQuarantinedBlob_MissingHash(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	body := map[string]bool{"approved": true}
	bodyBytes, _ := json.Marshal(body)

	// With Gin routing, //review would route to :hash with hash=""
	// The controller returns 400 Bad Request for empty hash
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/quarantine//review", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestGetProviders tests the GetProviders endpoint
func TestGetProviders(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.RegisterProvider(&mockProvider{
		name:       "provider1",
		mimeTypes:  []string{"image/jpeg", "image/png"},
		categories: []core.ContentCategory{core.CategoryCSAM, core.CategoryExplicitAdult},
		available:  true,
	})
	aiMod.RegisterProvider(&mockProvider{
		name:       "provider2",
		mimeTypes:  []string{"video/mp4"},
		categories: []core.ContentCategory{core.CategoryViolence},
		available:  false,
	})

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/providers", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["enabled"].(bool))
	providers := response["providers"].([]interface{})
	assert.Len(t, providers, 2)

	// Check first provider
	provider1 := providers[0].(map[string]interface{})
	assert.Equal(t, "provider1", provider1["name"])
	assert.True(t, provider1["available"].(bool))

	// Check second provider
	provider2 := providers[1].(map[string]interface{})
	assert.Equal(t, "provider2", provider2["name"])
	assert.False(t, provider2["available"].(bool))
}

// TestGetProviders_Disabled tests GetProviders when AI moderation is disabled
func TestGetProviders_Disabled(t *testing.T) {
	r := setupAIModerationRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/providers", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestScanBlob tests the ScanBlob endpoint
func TestScanBlob(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.scanAsyncQueueID = "queue-123"

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/scan/testhash?mime_type=image/jpeg", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "scan queued", response["message"])
	assert.Equal(t, "testhash", response["hash"])
	assert.Equal(t, "queue-123", response["queue_id"])
}

// TestScanBlob_MissingHash tests ScanBlob with missing hash
func TestScanBlob_MissingHash(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/scan/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestScanBlob_QueueError tests ScanBlob with queue error
func TestScanBlob_QueueError(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.scanAsyncError = core.ErrBlobNotFound

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/scan/testhash", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestGetScanResult tests the GetScanResult endpoint
func TestGetScanResult(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.scanResults["testhash"] = &core.ScanResult{
		Hash:              "testhash",
		Provider:          "test_provider",
		RecommendedAction: core.ScanActionAllow,
		Confidence:        core.ConfidenceVeryLow,
		ScannedAt:         time.Now().Unix(),
	}

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/scan/testhash", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result core.ScanResult
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, "testhash", result.Hash)
	assert.Equal(t, "test_provider", result.Provider)
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
}

// TestGetScanResult_NotFound tests GetScanResult with non-existent hash
func TestGetScanResult_NotFound(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/scan/nonexistent", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGetScanResult_MissingHash tests GetScanResult with missing hash
func TestGetScanResult_MissingHash(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/scan/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGetQueueStatus tests the GetQueueStatus endpoint
func TestGetQueueStatus(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.queueSize = 42

	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/queue", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, float64(42), response["queue_size"])
}

// TestGetQueueStatus_Disabled tests GetQueueStatus when AI moderation is disabled
func TestGetQueueStatus_Disabled(t *testing.T) {
	r := setupAIModerationRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/queue", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestRegisterAIModerationRoutes_NilController tests route registration with nil controller
func TestRegisterAIModerationRoutes_NilController(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	adminMiddleware := func(c *gin.Context) {
		c.Next()
	}

	// Should not panic with nil controller
	RegisterAIModerationRoutes(r, nil, adminMiddleware)

	// Routes should not be registered
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestRegisterAIModerationRoutes_NilService tests route registration with nil service
func TestRegisterAIModerationRoutes_NilService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	controller := NewAIModerationController(nil)
	adminMiddleware := func(c *gin.Context) {
		c.Next()
	}

	// Should not panic with nil service
	RegisterAIModerationRoutes(r, controller, adminMiddleware)

	// Routes should not be registered
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestReviewQuarantinedBlob_WithoutReviewerPubkey tests review without pubkey in context
func TestReviewQuarantinedBlob_WithoutReviewerPubkey(t *testing.T) {
	aiMod := newMockAIModerationService()
	aiMod.quarantinedBlobs["testhash"] = &core.QuarantinedBlob{
		Hash:      "testhash",
		Pubkey:    "testpubkey",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	controller := NewAIModerationController(aiMod)

	// Middleware that doesn't set pubkey
	adminMiddleware := func(c *gin.Context) {
		c.Next()
	}

	RegisterAIModerationRoutes(r, controller, adminMiddleware)

	body := map[string]bool{"approved": true}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/admin/ai-moderation/quarantine/testhash/review", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Should use "admin" as default
	blob := aiMod.quarantinedBlobs["testhash"]
	assert.Equal(t, "admin", blob.ReviewedBy)
}

// TestListQuarantined_Empty tests listing when no blobs are quarantined
func TestListQuarantined_Empty(t *testing.T) {
	aiMod := newMockAIModerationService()
	r := setupAIModerationRouter(aiMod)

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-moderation/quarantine", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Blobs  []*core.QuarantinedBlob `json:"blobs"`
		Count  int                     `json:"count"`
		Limit  int                     `json:"limit"`
		Offset int                     `json:"offset"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, 0, response.Count)
	assert.Len(t, response.Blobs, 0)
}
