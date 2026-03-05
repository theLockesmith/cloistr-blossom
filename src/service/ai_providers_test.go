package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// TestHashBlocklistProvider_Name tests provider name
func TestHashBlocklistProvider_Name(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)

	assert.Equal(t, "hash_blocklist", provider.Name())
}

// TestHashBlocklistProvider_SupportedMimeTypes tests supported MIME types
func TestHashBlocklistProvider_SupportedMimeTypes(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)

	mimeTypes := provider.SupportedMimeTypes()
	assert.Contains(t, mimeTypes, "image/")
	assert.Contains(t, mimeTypes, "video/")
}

// TestHashBlocklistProvider_SupportedCategories tests supported categories
func TestHashBlocklistProvider_SupportedCategories(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)

	categories := provider.SupportedCategories()
	assert.Contains(t, categories, core.CategoryCSAM)
	assert.Contains(t, categories, core.CategoryCopyrightRisk)
}

// TestHashBlocklistProvider_IsAvailable tests availability check
func TestHashBlocklistProvider_IsAvailable(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)
	ctx := context.Background()

	// Hash blocklist is always available
	assert.True(t, provider.IsAvailable(ctx))
}

// TestHashBlocklistProvider_AddHash tests adding hashes to blocklist
func TestHashBlocklistProvider_AddHash(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)
	ctx := context.Background()

	// Add a hash
	provider.AddHash("abc123", "test reason")

	// Scan with the hash
	req := &core.ScanRequest{
		Hash:     "abc123",
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
	assert.Len(t, result.Detections, 1)
	assert.Equal(t, core.CategoryCSAM, result.Detections[0].Category)
	assert.Equal(t, float64(100), result.Detections[0].Confidence)
	assert.Contains(t, result.Detections[0].Description, "test reason")
}

// TestHashBlocklistProvider_Scan_NotBlocked tests scanning non-blocked hash
func TestHashBlocklistProvider_Scan_NotBlocked(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "cleanhash",
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Len(t, result.Detections, 0)
	assert.Equal(t, core.ConfidenceVeryLow, result.Confidence)
}

// TestHashBlocklistProvider_Scan_CaseInsensitive tests case-insensitive matching
func TestHashBlocklistProvider_Scan_CaseInsensitive(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)
	ctx := context.Background()

	// Add lowercase hash
	provider.AddHash("abc123", "blocked")

	// Scan with uppercase
	req := &core.ScanRequest{
		Hash:     "ABC123",
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
}

// TestHashBlocklistProvider_Scan_WithData tests scanning with data instead of hash
func TestHashBlocklistProvider_Scan_WithData(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)
	ctx := context.Background()

	// Compute hash of test data
	data := []byte("test content")
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	// Add to blocklist
	provider.AddHash(hash, "blocked content")

	// Scan with data
	req := &core.ScanRequest{
		Hash:     "",
		Data:     data,
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
}

// TestHashBlocklistProvider_LoadHashList tests loading hash list from data
func TestHashBlocklistProvider_LoadHashList(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)

	hashList := `
# Comment line
hash1,reason 1
hash2,reason 2
hash3
# Another comment

hash4,reason 4
`

	err := provider.LoadHashList([]byte(hashList))
	require.NoError(t, err)

	// Verify hashes were loaded
	ctx := context.Background()

	tests := []struct {
		hash          string
		shouldBlock   bool
		expectedReason string
	}{
		{"hash1", true, "reason 1"},
		{"hash2", true, "reason 2"},
		{"hash3", true, "blocklist match"},
		{"hash4", true, "reason 4"},
		{"hash5", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			req := &core.ScanRequest{
				Hash:     tt.hash,
				MimeType: "image/jpeg",
			}

			result, err := provider.Scan(ctx, req)
			require.NoError(t, err)

			if tt.shouldBlock {
				assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
				assert.Contains(t, result.Detections[0].Description, tt.expectedReason)
			} else {
				assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
			}
		})
	}
}

// TestHashBlocklistProvider_LoadHashList_EmptyLines tests handling empty lines
func TestHashBlocklistProvider_LoadHashList_EmptyLines(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewHashBlocklistProvider(log)

	hashList := `

hash1,reason 1

hash2,reason 2

`

	err := provider.LoadHashList([]byte(hashList))
	require.NoError(t, err)

	// Verify both hashes loaded
	ctx := context.Background()
	req := &core.ScanRequest{Hash: "hash1", MimeType: "image/jpeg"}
	result, _ := provider.Scan(ctx, req)
	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)

	req = &core.ScanRequest{Hash: "hash2", MimeType: "image/jpeg"}
	result, _ = provider.Scan(ctx, req)
	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
}

// TestAWSRekognitionProvider_Name tests provider name
func TestAWSRekognitionProvider_Name(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewAWSRekognitionProvider("key", "secret", "us-east-1", log)

	assert.Equal(t, "aws_rekognition", provider.Name())
}

// TestAWSRekognitionProvider_SupportedMimeTypes tests supported MIME types
func TestAWSRekognitionProvider_SupportedMimeTypes(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewAWSRekognitionProvider("key", "secret", "us-east-1", log)

	mimeTypes := provider.SupportedMimeTypes()
	assert.Contains(t, mimeTypes, "image/jpeg")
	assert.Contains(t, mimeTypes, "image/png")
}

// TestAWSRekognitionProvider_SupportedCategories tests supported categories
func TestAWSRekognitionProvider_SupportedCategories(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewAWSRekognitionProvider("key", "secret", "us-east-1", log)

	categories := provider.SupportedCategories()
	assert.Contains(t, categories, core.CategoryExplicitAdult)
	assert.Contains(t, categories, core.CategoryViolence)
	assert.Contains(t, categories, core.CategoryHate)
	assert.Contains(t, categories, core.CategoryDrugs)
	assert.Contains(t, categories, core.CategoryWeapons)
}

// TestAWSRekognitionProvider_IsAvailable tests availability with and without credentials
func TestAWSRekognitionProvider_IsAvailable(t *testing.T) {
	log, _ := zap.NewDevelopment()
	ctx := context.Background()

	// With credentials
	provider1 := NewAWSRekognitionProvider("key", "secret", "us-east-1", log)
	assert.True(t, provider1.IsAvailable(ctx))

	// Without credentials
	provider2 := NewAWSRekognitionProvider("", "", "us-east-1", log)
	assert.False(t, provider2.IsAvailable(ctx))
}

// TestAWSRekognitionProvider_Scan_NoCredentials tests scanning without credentials
func TestAWSRekognitionProvider_Scan_NoCredentials(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewAWSRekognitionProvider("", "", "us-east-1", log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Contains(t, result.Error, "not configured")
}

// TestAWSRekognitionProvider_Scan_Stub tests stub implementation
func TestAWSRekognitionProvider_Scan_Stub(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewAWSRekognitionProvider("key", "secret", "us-east-1", log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Stub returns allow with very low confidence
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Equal(t, core.ConfidenceVeryLow, result.Confidence)
}

// TestGoogleVisionProvider_Name tests provider name
func TestGoogleVisionProvider_Name(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewGoogleVisionProvider("apikey", "project-id", log)

	assert.Equal(t, "google_vision", provider.Name())
}

// TestGoogleVisionProvider_SupportedMimeTypes tests supported MIME types
func TestGoogleVisionProvider_SupportedMimeTypes(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewGoogleVisionProvider("apikey", "project-id", log)

	mimeTypes := provider.SupportedMimeTypes()
	assert.Contains(t, mimeTypes, "image/jpeg")
	assert.Contains(t, mimeTypes, "image/png")
	assert.Contains(t, mimeTypes, "image/gif")
	assert.Contains(t, mimeTypes, "image/webp")
}

// TestGoogleVisionProvider_SupportedCategories tests supported categories
func TestGoogleVisionProvider_SupportedCategories(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewGoogleVisionProvider("apikey", "project-id", log)

	categories := provider.SupportedCategories()
	assert.Contains(t, categories, core.CategoryExplicitAdult)
	assert.Contains(t, categories, core.CategoryViolence)
	assert.Contains(t, categories, core.CategoryHate)
}

// TestGoogleVisionProvider_IsAvailable tests availability check
func TestGoogleVisionProvider_IsAvailable(t *testing.T) {
	log, _ := zap.NewDevelopment()
	ctx := context.Background()

	// With API key
	provider1 := NewGoogleVisionProvider("apikey", "project-id", log)
	assert.True(t, provider1.IsAvailable(ctx))

	// Without API key
	provider2 := NewGoogleVisionProvider("", "project-id", log)
	assert.False(t, provider2.IsAvailable(ctx))
}

// TestGoogleVisionProvider_Scan_NoAPIKey tests scanning without API key
func TestGoogleVisionProvider_Scan_NoAPIKey(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewGoogleVisionProvider("", "project-id", log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Contains(t, result.Error, "not configured")
}

// TestGoogleVisionProvider_Scan_Stub tests stub implementation
func TestGoogleVisionProvider_Scan_Stub(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewGoogleVisionProvider("apikey", "project-id", log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Stub returns allow
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
}

// TestCustomAPIProvider_Name tests provider name
func TestCustomAPIProvider_Name(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", "http://localhost", "key", []string{"image/"}, log)

	assert.Equal(t, "custom", provider.Name())
}

// TestCustomAPIProvider_SupportedMimeTypes tests supported MIME types
func TestCustomAPIProvider_SupportedMimeTypes(t *testing.T) {
	log, _ := zap.NewDevelopment()
	mimeTypes := []string{"image/jpeg", "video/mp4"}
	provider := NewCustomAPIProvider("custom", "http://localhost", "key", mimeTypes, log)

	supported := provider.SupportedMimeTypes()
	assert.Equal(t, mimeTypes, supported)
}

// TestCustomAPIProvider_SupportedCategories tests supported categories
func TestCustomAPIProvider_SupportedCategories(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", "http://localhost", "key", []string{"image/"}, log)

	categories := provider.SupportedCategories()
	assert.Contains(t, categories, core.CategoryCSAM)
	assert.Contains(t, categories, core.CategoryExplicitAdult)
	assert.Contains(t, categories, core.CategoryViolence)
}

// TestCustomAPIProvider_IsAvailable tests availability check
func TestCustomAPIProvider_IsAvailable(t *testing.T) {
	log, _ := zap.NewDevelopment()
	ctx := context.Background()

	// With endpoint
	provider1 := NewCustomAPIProvider("custom", "http://localhost", "key", []string{"image/"}, log)
	assert.True(t, provider1.IsAvailable(ctx))

	// Without endpoint
	provider2 := NewCustomAPIProvider("custom", "", "key", []string{"image/"}, log)
	assert.False(t, provider2.IsAvailable(ctx))
}

// TestCustomAPIProvider_Scan_NoEndpoint tests scanning without endpoint
func TestCustomAPIProvider_Scan_NoEndpoint(t *testing.T) {
	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", "", "key", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Contains(t, result.Error, "endpoint not configured")
}

// TestCustomAPIProvider_Scan_Success tests successful API call
func TestCustomAPIProvider_Scan_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer testkey", r.Header.Get("Authorization"))

		// Return mock response
		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{
				{
					Category:   "explicit_adult",
					Confidence: 75,
					Label:      "Adult content detected",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.Detections, 1)
	assert.Equal(t, core.CategoryExplicitAdult, result.Detections[0].Category)
	assert.Equal(t, float64(75), result.Detections[0].Confidence)
	assert.Equal(t, "Adult content detected", result.Detections[0].Description)
	assert.Equal(t, core.ScanActionQuarantine, result.RecommendedAction)
}

// TestCustomAPIProvider_Scan_BlockAction tests API response triggering block
func TestCustomAPIProvider_Scan_BlockAction(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{
				{
					Category:   "csam",
					Confidence: 95,
					Label:      "CSAM detected",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionBlock, result.RecommendedAction)
	assert.Equal(t, core.CategoryCSAM, result.Detections[0].Category)
}

// TestCustomAPIProvider_Scan_FlagAction tests API response triggering flag
func TestCustomAPIProvider_Scan_FlagAction(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{
				{
					Category:   "violence",
					Confidence: 35,
					Label:      "Possible violence",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionFlag, result.RecommendedAction)
}

// TestCustomAPIProvider_Scan_NoDetections tests API returning no detections
func TestCustomAPIProvider_Scan_NoDetections(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Len(t, result.Detections, 0)
}

// TestCustomAPIProvider_Scan_APIError tests handling API errors
func TestCustomAPIProvider_Scan_APIError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return allow on error
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Contains(t, result.Error, "500")
}

// TestCustomAPIProvider_Scan_InvalidJSON tests handling invalid JSON response
func TestCustomAPIProvider_Scan_InvalidJSON(t *testing.T) {
	// Create mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return allow on decode error
	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
	assert.Contains(t, result.Error, "failed to decode")
}

// TestCustomAPIProvider_Scan_WithoutAPIKey tests scanning without API key
func TestCustomAPIProvider_Scan_WithoutAPIKey(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no Authorization header
		assert.Empty(t, r.Header.Get("Authorization"))

		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "", []string{"image/"}, log) // No API key
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, core.ScanActionAllow, result.RecommendedAction)
}

// TestCustomAPIProvider_Scan_MultipleDetections tests multiple detections
func TestCustomAPIProvider_Scan_MultipleDetections(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := CustomAPIResponse{
			Detections: []struct {
				Category   string  `json:"category"`
				Confidence float64 `json:"confidence"`
				Label      string  `json:"label"`
			}{
				{Category: "explicit_adult", Confidence: 60, Label: "Adult content"},
				{Category: "violence", Confidence: 70, Label: "Violence"},
				{Category: "drugs", Confidence: 45, Label: "Drug paraphernalia"},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	log, _ := zap.NewDevelopment()
	provider := NewCustomAPIProvider("custom", server.URL, "testkey", []string{"image/"}, log)
	ctx := context.Background()

	req := &core.ScanRequest{
		Hash:     "testhash",
		Data:     []byte("test data"),
		MimeType: "image/jpeg",
	}

	result, err := provider.Scan(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.Detections, 3)
	assert.Equal(t, core.ConfidenceMedium, result.Confidence)
	assert.Equal(t, core.ScanActionQuarantine, result.RecommendedAction)
}
