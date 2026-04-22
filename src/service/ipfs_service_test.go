package service

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

func TestIPFSServiceNotConfigured(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ipfs-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	// Create service with IPFS disabled
	conf := &config.IPFSConfig{
		Enabled: false,
	}

	svc, err := NewIPFSService(localStorage, memCache, conf, log)
	require.NoError(t, err)

	// Should not be configured
	assert.False(t, svc.IsConfigured(), "IPFS should not be configured")

	// All operations should return ErrIPFSNotConfigured
	_, err = svc.PinBlob(nil, "hash", "name")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	err = svc.UnpinBlob(nil, "hash")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	_, err = svc.GetPinStatus(nil, "hash")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	_, err = svc.ListPins(nil, "", 10)
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	// Gateway URL still works (just formats a string) with default gateway
	gatewayURL := svc.GetIPFSGatewayURL("cidxxx")
	assert.Contains(t, gatewayURL, "cidxxx")
}

func TestIPFSServiceMissingCredentials(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ipfs-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	// Create service with enabled but missing credentials
	conf := &config.IPFSConfig{
		Enabled:     true,
		Endpoint:    "", // Missing
		BearerToken: "", // Missing
	}

	svc, err := NewIPFSService(localStorage, memCache, conf, log)
	require.NoError(t, err)

	// Should not be configured without credentials
	assert.False(t, svc.IsConfigured(), "IPFS should not be configured without credentials")
}

func TestIPFSServiceGatewayURL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ipfs-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	// Create configured service
	conf := &config.IPFSConfig{
		Enabled:     true,
		Endpoint:    "https://api.pinata.cloud/psa",
		BearerToken: "test-token",
		GatewayURL:  "https://gateway.pinata.cloud/ipfs/",
	}

	svc, err := NewIPFSService(localStorage, memCache, conf, log)
	require.NoError(t, err)

	// Should be configured
	assert.True(t, svc.IsConfigured(), "IPFS should be configured")

	// Test gateway URL generation
	gatewayURL := svc.GetIPFSGatewayURL("bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi")
	assert.Equal(t, "https://gateway.pinata.cloud/ipfs/bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi", gatewayURL)
}

func TestIPFSServiceGatewayURLTrailingSlash(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ipfs-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	// Create configured service without trailing slash
	conf := &config.IPFSConfig{
		Enabled:     true,
		Endpoint:    "https://api.pinata.cloud/psa",
		BearerToken: "test-token",
		GatewayURL:  "https://ipfs.io/ipfs", // No trailing slash
	}

	svc, err := NewIPFSService(localStorage, memCache, conf, log)
	require.NoError(t, err)

	// Gateway URL should still be correct
	gatewayURL := svc.GetIPFSGatewayURL("cid123")
	assert.Equal(t, "https://ipfs.io/ipfs/cid123", gatewayURL)
}

func TestCalculateCID(t *testing.T) {
	// Test CID calculation
	testData := []byte("Hello, IPFS!")

	cid, err := calculateCID(testData)
	require.NoError(t, err)

	// CID should be valid
	assert.True(t, cid.Defined(), "CID should be defined")

	// CID should be CIDv1
	assert.Equal(t, uint64(1), cid.Version(), "Should be CIDv1")

	// Same data should produce same CID
	cid2, err := calculateCID(testData)
	require.NoError(t, err)
	assert.Equal(t, cid.String(), cid2.String(), "Same data should produce same CID")

	// Different data should produce different CID
	differentData := []byte("Different content")
	cid3, err := calculateCID(differentData)
	require.NoError(t, err)
	assert.NotEqual(t, cid.String(), cid3.String(), "Different data should produce different CID")
}

func TestIPFSPinStatusTypes(t *testing.T) {
	// Test status constants
	assert.Equal(t, core.IPFSPinStatus("queued"), core.IPFSPinStatusQueued)
	assert.Equal(t, core.IPFSPinStatus("pinning"), core.IPFSPinStatusPinning)
	assert.Equal(t, core.IPFSPinStatus("pinned"), core.IPFSPinStatusPinned)
	assert.Equal(t, core.IPFSPinStatus("failed"), core.IPFSPinStatusFailed)
}

func TestIPFSPinTypes(t *testing.T) {
	// Test IPFSPin struct
	pin := core.IPFSPin{
		BlobHash:  "abc123",
		CID:       "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		Name:      "test-pin",
		Status:    core.IPFSPinStatusPinned,
		RequestID: "req-123",
		Meta: map[string]string{
			"source": "test",
		},
		CreatedAt: 1234567890,
		PinnedAt:  1234567891,
	}

	assert.Equal(t, "abc123", pin.BlobHash)
	assert.Equal(t, "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi", pin.CID)
	assert.Equal(t, core.IPFSPinStatusPinned, pin.Status)
	assert.Equal(t, "test", pin.Meta["source"])
}

func TestNoopIPFSService(t *testing.T) {
	svc := &noopIPFSService{}

	assert.False(t, svc.IsConfigured())
	assert.Empty(t, svc.GetIPFSGatewayURL("cid"))

	_, err := svc.PinBlob(nil, "hash", "name")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	err = svc.UnpinBlob(nil, "hash")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	_, err = svc.GetPinStatus(nil, "hash")
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)

	_, err = svc.ListPins(nil, "", 10)
	assert.ErrorIs(t, err, core.ErrIPFSNotConfigured)
}
