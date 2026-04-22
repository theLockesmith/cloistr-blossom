package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

func TestCDNServiceDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdn-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	log, _ := zap.NewDevelopment()

	// CDN disabled
	svc, err := NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig:  &config.CDNConfig{Enabled: false},
		CDNBaseURL: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Should not be enabled
	assert.False(t, svc.IsEnabled())
	assert.False(t, svc.ShouldRedirect())

	// Should return API URL when disabled
	url, err := svc.GetBlobURL(context.Background(), "abc123", "")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8000/abc123", url)
}

func TestCDNServiceEnabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdn-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	log, _ := zap.NewDevelopment()

	// CDN enabled with public URL
	svc, err := NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig: &config.CDNConfig{
			Enabled:   true,
			PublicURL: "https://cdn.example.com",
			Redirect:  true,
		},
		CDNBaseURL: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Should be enabled
	assert.True(t, svc.IsEnabled())
	assert.True(t, svc.ShouldRedirect())

	// Should return CDN URL when enabled
	url, err := svc.GetBlobURL(context.Background(), "abc123", "")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example.com/abc123", url)

	// GetPublicURL should work
	publicURL := svc.GetPublicURL("def456")
	assert.Equal(t, "https://cdn.example.com/def456", publicURL)
}

func TestCDNServicePresignedURLsFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdn-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	log, _ := zap.NewDevelopment()

	// CDN enabled with presigned URLs but local storage doesn't support it
	svc, err := NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig: &config.CDNConfig{
			Enabled:       true,
			PresignedURLs: true,
			// No PublicURL, so should try presigned then fall back
		},
		CDNBaseURL: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Should be enabled
	assert.True(t, svc.IsEnabled())

	// Local storage doesn't support presigned URLs, so should fall back to API URL
	url, err := svc.GetBlobURL(context.Background(), "abc123", "")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8000/abc123", url)

	// Direct presigned URL request should fail
	_, err = svc.GetPresignedURL(context.Background(), "abc123", time.Hour)
	assert.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrPresignedURLNotSupported)
}

func TestCDNServiceExpiryParsing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdn-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	log, _ := zap.NewDevelopment()

	// Valid duration
	_, err = NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig: &config.CDNConfig{
			Enabled:         true,
			PresignedExpiry: "24h",
		},
		CDNBaseURL: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Invalid duration
	_, err = NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig: &config.CDNConfig{
			Enabled:         true,
			PresignedExpiry: "invalid",
		},
		CDNBaseURL: "http://localhost:8000",
	}, log)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid presigned_expiry")
}

func TestCDNServiceNilConfig(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdn-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	log, _ := zap.NewDevelopment()

	// Nil CDN config should work
	svc, err := NewCDNService(localStorage, CDNServiceConfig{
		CDNConfig:  nil,
		CDNBaseURL: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	assert.False(t, svc.IsEnabled())
	assert.False(t, svc.ShouldRedirect())
}
