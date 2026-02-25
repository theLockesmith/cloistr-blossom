package gin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupTestRouter creates a gin router with auth middleware for testing
func setupTestRouter(action string) (*gin.Engine, *zap.Logger) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	r := gin.New()
	r.Use(nostrAuthMiddleware(action, logger))
	r.GET("/test", func(c *gin.Context) {
		pk, _ := c.Get("pk")
		x, _ := c.Get("x")
		c.JSON(http.StatusOK, gin.H{
			"pk": pk,
			"x":  x,
		})
	})

	return r, logger
}

// createValidAuthEvent creates a valid Nostr auth event for testing
func createValidAuthEvent(action string, xTag string, expirationOffset time.Duration) *nostr.Event {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags:      nostr.Tags{},
		Content:   "",
	}

	// Add expiration tag
	expiration := time.Now().Add(expirationOffset).Unix()
	ev.Tags = append(ev.Tags, nostr.Tag{"expiration", strconv.FormatInt(expiration, 10)})

	// Add t tag (action)
	ev.Tags = append(ev.Tags, nostr.Tag{"t", action})

	// Add x tag if provided
	if xTag != "" {
		ev.Tags = append(ev.Tags, nostr.Tag{"x", xTag})
	}

	ev.Sign(sk)

	return ev
}

// encodeAuthEvent encodes an event as base64 for Authorization header
func encodeAuthEvent(ev *nostr.Event) (string, error) {
	eventBytes, err := json.Marshal(ev)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(eventBytes), nil
}

// TestNostrAuthMiddleware_ValidAuth tests successful authentication
func TestNostrAuthMiddleware_ValidAuth(t *testing.T) {
	r, _ := setupTestRouter("upload")

	ev := createValidAuthEvent("upload", "sha256hash", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ev.PubKey, response["pk"])
	assert.Equal(t, "sha256hash", response["x"])
}

// TestNostrAuthMiddleware_MissingAuthHeader tests missing Authorization header
func TestNostrAuthMiddleware_MissingAuthHeader(t *testing.T) {
	r, _ := setupTestRouter("upload")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_MissingNostrPrefix tests Authorization header without "Nostr " prefix
func TestNostrAuthMiddleware_MissingNostrPrefix(t *testing.T) {
	r, _ := setupTestRouter("upload")

	ev := createValidAuthEvent("upload", "sha256hash", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+authHeader) // Wrong prefix

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_InvalidBase64 tests invalid base64 encoding
func TestNostrAuthMiddleware_InvalidBase64(t *testing.T) {
	r, _ := setupTestRouter("upload")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr invalid!!!base64")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_InvalidJSON tests malformed JSON in event
func TestNostrAuthMiddleware_InvalidJSON(t *testing.T) {
	r, _ := setupTestRouter("upload")

	invalidJSON := base64.StdEncoding.EncodeToString([]byte("{invalid json"))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+invalidJSON)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_InvalidSignature tests event with invalid signature
func TestNostrAuthMiddleware_InvalidSignature(t *testing.T) {
	r, _ := setupTestRouter("upload")

	ev := createValidAuthEvent("upload", "sha256hash", 1*time.Hour)

	// Tamper with the signature
	ev.Sig = "0000000000000000000000000000000000000000000000000000000000000000"

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_WrongKind tests event with wrong kind
func TestNostrAuthMiddleware_WrongKind(t *testing.T) {
	r, _ := setupTestRouter("upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      1, // Wrong kind (should be 24242)
		Tags: nostr.Tags{
			{"expiration", strconv.FormatInt(time.Now().Add(1 * time.Hour).Unix(), 10)},
			{"t", "upload"},
			{"x", "sha256hash"},
		},
		Content: "",
	}
	ev.Sign(sk)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_FutureCreatedAt tests event with future created_at timestamp
func TestNostrAuthMiddleware_FutureCreatedAt(t *testing.T) {
	r, _ := setupTestRouter("upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	// Create event with future timestamp
	futureTime := time.Now().Add(1 * time.Hour)
	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Timestamp(futureTime.Unix()),
		Kind:      24242,
		Tags: nostr.Tags{
			{"expiration", strconv.FormatInt(time.Now().Add(2 * time.Hour).Unix(), 10)},
			{"t", "upload"},
			{"x", "sha256hash"},
		},
		Content: "",
	}
	ev.Sign(sk)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_MissingExpirationTag tests event without expiration tag
func TestNostrAuthMiddleware_MissingExpirationTag(t *testing.T) {
	r, _ := setupTestRouter("upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"t", "upload"},
			{"x", "sha256hash"},
			// Missing expiration tag
		},
		Content: "",
	}
	ev.Sign(sk)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_MissingTTag tests event without t tag
func TestNostrAuthMiddleware_MissingTTag(t *testing.T) {
	r, _ := setupTestRouter("upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"expiration", strconv.FormatInt(time.Now().Add(1 * time.Hour).Unix(), 10)},
			{"x", "sha256hash"},
			// Missing t tag
		},
		Content: "",
	}
	ev.Sign(sk)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_ExpiredEvent tests event with expired expiration tag
func TestNostrAuthMiddleware_ExpiredEvent(t *testing.T) {
	r, _ := setupTestRouter("upload")

	// Create event with past expiration
	ev := createValidAuthEvent("upload", "sha256hash", -1*time.Hour)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_WrongAction tests event with mismatched action
func TestNostrAuthMiddleware_WrongAction(t *testing.T) {
	r, _ := setupTestRouter("upload")

	// Create event with "delete" action but middleware expects "upload"
	ev := createValidAuthEvent("delete", "sha256hash", 1*time.Hour)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_UploadWithoutXTag tests upload action without required x tag
func TestNostrAuthMiddleware_UploadWithoutXTag(t *testing.T) {
	r, _ := setupTestRouter("upload")

	// Create event without x tag for upload action
	ev := createValidAuthEvent("upload", "", 1*time.Hour)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_DeleteWithoutXTag tests delete action without required x tag
func TestNostrAuthMiddleware_DeleteWithoutXTag(t *testing.T) {
	r, _ := setupTestRouter("delete")

	// Create event without x tag for delete action
	ev := createValidAuthEvent("delete", "", 1*time.Hour)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_DeleteActionValid tests valid delete action with x tag
func TestNostrAuthMiddleware_DeleteActionValid(t *testing.T) {
	r, _ := setupTestRouter("delete")

	ev := createValidAuthEvent("delete", "sha256hash", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ev.PubKey, response["pk"])
	assert.Equal(t, "sha256hash", response["x"])
}

// TestNostrAuthMiddleware_MirrorActionValid tests valid mirror action
func TestNostrAuthMiddleware_MirrorActionValid(t *testing.T) {
	r, _ := setupTestRouter("upload")

	// Mirror uses upload action with x tag
	ev := createValidAuthEvent("upload", "sha256hash", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestNostrAuthMiddleware_MediaActionValid tests valid media action
func TestNostrAuthMiddleware_MediaActionValid(t *testing.T) {
	r, _ := setupTestRouter("media")

	// Media action doesn't strictly require x tag
	ev := createValidAuthEvent("media", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ev.PubKey, response["pk"])
	assert.Equal(t, "", response["x"])
}

// TestNostrAuthMiddleware_ListActionValid tests valid list action without x tag
func TestNostrAuthMiddleware_ListActionValid(t *testing.T) {
	r, _ := setupTestRouter("list")

	// List action doesn't require x tag
	ev := createValidAuthEvent("list", "", 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestNostrAuthMiddleware_MultipleRequests tests that middleware handles multiple sequential requests
func TestNostrAuthMiddleware_MultipleRequests(t *testing.T) {
	r, _ := setupTestRouter("upload")

	// First valid request
	ev1 := createValidAuthEvent("upload", "hash1", 1*time.Hour)
	authHeader1, err := encodeAuthEvent(ev1)
	require.NoError(t, err)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Nostr "+authHeader1)

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusOK, w1.Code)

	// Second valid request with different hash
	ev2 := createValidAuthEvent("upload", "hash2", 1*time.Hour)
	authHeader2, err := encodeAuthEvent(ev2)
	require.NoError(t, err)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Nostr "+authHeader2)

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)

	// Third invalid request
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.Header.Set("Authorization", "Nostr invalid")

	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)

	assert.Equal(t, http.StatusUnauthorized, w3.Code)
}

// TestNostrAuthMiddleware_EmptyXTagValue tests event with empty x tag value
func TestNostrAuthMiddleware_EmptyXTagValue(t *testing.T) {
	r, _ := setupTestRouter("upload")

	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"expiration", strconv.FormatInt(time.Now().Add(1 * time.Hour).Unix(), 10)},
			{"t", "upload"},
			{"x", ""}, // Empty x tag value
		},
		Content: "",
	}
	ev.Sign(sk)

	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Empty x tag value should be treated as missing for upload action
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestNostrAuthMiddleware_MalformedTags tests events with malformed tags
func TestNostrAuthMiddleware_MalformedTags(t *testing.T) {
	tests := []struct {
		name string
		tags nostr.Tags
	}{
		{
			name: "expiration tag with wrong length",
			tags: nostr.Tags{
				{"expiration"}, // Missing value
				{"t", "upload"},
				{"x", "sha256hash"},
			},
		},
		{
			name: "t tag with wrong length",
			tags: nostr.Tags{
				{"expiration", strconv.FormatInt(time.Now().Add(1 * time.Hour).Unix(), 10)},
				{"t"}, // Missing value
				{"x", "sha256hash"},
			},
		},
		{
			name: "x tag with wrong length",
			tags: nostr.Tags{
				{"expiration", strconv.FormatInt(time.Now().Add(1 * time.Hour).Unix(), 10)},
				{"t", "upload"},
				{"x"}, // Missing value
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := setupTestRouter("upload")

			sk := nostr.GeneratePrivateKey()
			pk, _ := nostr.GetPublicKey(sk)

			ev := &nostr.Event{
				PubKey:    pk,
				CreatedAt: nostr.Now(),
				Kind:      24242,
				Tags:      tt.tags,
				Content:   "",
			}
			ev.Sign(sk)

			authHeader, err := encodeAuthEvent(ev)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Nostr "+authHeader)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

// TestNostrAuthMiddleware_EdgeCaseTimestamps tests edge cases for timestamp validation
func TestNostrAuthMiddleware_EdgeCaseTimestamps(t *testing.T) {
	tests := []struct {
		name              string
		createdAt         time.Time
		expirationOffset  time.Duration
		expectedStatus    int
	}{
		{
			name:             "expiration exactly at current time",
			createdAt:        time.Now().Add(-1 * time.Minute),
			expirationOffset: 0,
			expectedStatus:   http.StatusOK, // Expiration at current time is still valid (uses < comparison)
		},
		{
			name:             "expiration 1 second in future",
			createdAt:        time.Now().Add(-1 * time.Minute),
			expirationOffset: 1 * time.Second,
			expectedStatus:   http.StatusOK,
		},
		{
			name:             "very long expiration",
			createdAt:        time.Now().Add(-1 * time.Minute),
			expirationOffset: 365 * 24 * time.Hour, // 1 year
			expectedStatus:   http.StatusOK,
		},
		{
			name:             "created_at exactly at current time",
			createdAt:        time.Now(),
			expirationOffset: 1 * time.Hour,
			expectedStatus:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := setupTestRouter("upload")

			sk := nostr.GeneratePrivateKey()
			pk, _ := nostr.GetPublicKey(sk)

			expiration := time.Now().Add(tt.expirationOffset).Unix()

			ev := &nostr.Event{
				PubKey:    pk,
				CreatedAt: nostr.Timestamp(tt.createdAt.Unix()),
				Kind:      24242,
				Tags: nostr.Tags{
					{"expiration", strconv.FormatInt(expiration, 10)},
					{"t", "upload"},
					{"x", "sha256hash"},
				},
				Content: "",
			}
			ev.Sign(sk)

			authHeader, err := encodeAuthEvent(ev)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Nostr "+authHeader)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// TestNostrAuthMiddleware_ContextValues tests that pk and x values are correctly set in context
func TestNostrAuthMiddleware_ContextValues(t *testing.T) {
	r, _ := setupTestRouter("upload")

	expectedHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	ev := createValidAuthEvent("upload", expectedHash, 1*time.Hour)
	authHeader, err := encodeAuthEvent(ev)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Nostr "+authHeader)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify pk is set correctly
	assert.Equal(t, ev.PubKey, response["pk"])

	// Verify x is set correctly
	assert.Equal(t, expectedHash, response["x"])
}
