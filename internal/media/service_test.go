package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockStorageBackend is a mock implementation of storage.StorageBackend for testing.
type MockStorageBackend struct {
	data map[string][]byte
}

func NewMockStorageBackend() *MockStorageBackend {
	return &MockStorageBackend{
		data: make(map[string][]byte),
	}
}

func (m *MockStorageBackend) Put(ctx context.Context, hash string, data io.Reader, size int64) error {
	buf := new(bytes.Buffer)
	_, err := io.Copy(buf, data)
	if err != nil {
		return err
	}
	m.data[hash] = buf.Bytes()
	return nil
}

func (m *MockStorageBackend) Get(ctx context.Context, hash string) (io.ReadCloser, error) {
	data, ok := m.data[hash]
	if !ok {
		return nil, fmt.Errorf("blob not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *MockStorageBackend) Delete(ctx context.Context, hash string) error {
	delete(m.data, hash)
	return nil
}

func (m *MockStorageBackend) Exists(ctx context.Context, hash string) (bool, error) {
	_, ok := m.data[hash]
	return ok, nil
}

func (m *MockStorageBackend) Size(ctx context.Context, hash string) (int64, error) {
	data, ok := m.data[hash]
	if !ok {
		return 0, fmt.Errorf("blob not found")
	}
	return int64(len(data)), nil
}

// Helper to create a test hash for an image
func hashData(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestNewMediaService(t *testing.T) {
	storage := NewMockStorageBackend()

	t.Run("with_cache", func(t *testing.T) {
		c := cache.NewMemoryCache(1024 * 1024) // 1MB
		service := NewMediaService(storage, c, 5*time.Minute)

		assert.NotNil(t, service)
		assert.Equal(t, storage, service.storage)
		assert.Equal(t, c, service.cache)
		assert.Equal(t, 5*time.Minute, service.cacheTTL)
		assert.NotNil(t, service.processor)
	})

	t.Run("without_cache", func(t *testing.T) {
		service := NewMediaService(storage, nil, 5*time.Minute)

		assert.NotNil(t, service)
		assert.NotNil(t, service.cache) // Should have fallback cache
		assert.NotNil(t, service.processor)
	})
}

func TestMediaService_GetImage_NoProcessing(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(200, 200, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Get image without processing
	result, err := service.GetImage(ctx, hash, nil)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, testData, result.Data)
	assert.Equal(t, "application/octet-stream", result.ContentType)
}

func TestMediaService_GetImage_WithProcessing(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(400, 400, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Get image with resizing
	result, err := service.GetImage(ctx, hash, &ProcessOptions{
		Width:  200,
		Height: 200,
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 200, result.Width)
	assert.Equal(t, 200, result.Height)
	assert.NotEqual(t, testData, result.Data) // Should be processed
}

func TestMediaService_GetImage_Caching(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(400, 400, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	opts := &ProcessOptions{
		Width:  200,
		Height: 200,
		Crop:   true,
	}

	// First request - should process and cache
	result1, err := service.GetImage(ctx, hash, opts)
	require.NoError(t, err)

	// Second request - should use cache
	result2, err := service.GetImage(ctx, hash, opts)
	require.NoError(t, err)

	// Data should be identical (cached)
	assert.Equal(t, result1.Data, result2.Data)
	// Note: Width/Height are not preserved in cache, only Data is cached
}

func TestMediaService_GetImage_CacheKeyVariants(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(400, 400, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Request different variants with significantly different sizes
	result1, err := service.GetImage(ctx, hash, &ProcessOptions{
		Width:  300,
		Height: 300,
	})
	require.NoError(t, err)

	result2, err := service.GetImage(ctx, hash, &ProcessOptions{
		Width:  50,
		Height: 50,
	})
	require.NoError(t, err)

	// Different sizes should produce different data lengths at minimum
	// (smaller image = smaller file size)
	assert.True(t, len(result2.Data) < len(result1.Data),
		"50x50 image should be smaller than 300x300 image")
}

func TestMediaService_GetImage_NotFound(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Try to get non-existent image
	_, err := service.GetImage(ctx, "nonexistent", nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get image from storage")
}

func TestMediaService_GetImage_FormatConversion(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a JPEG image
	testData := createTestImage(200, 200, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Convert to PNG
	result, err := service.GetImage(ctx, hash, &ProcessOptions{
		Format: FormatPNG,
	})

	require.NoError(t, err)
	assert.Equal(t, FormatPNG, result.Format)
	assert.Equal(t, "image/png", result.ContentType)
}

func TestMediaService_GetThumbnail(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(800, 600, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	tests := []struct {
		name string
		size ThumbnailSize
	}{
		{
			name: "small",
			size: ThumbnailSmall,
		},
		{
			name: "medium",
			size: ThumbnailMedium,
		},
		{
			name: "large",
			size: ThumbnailLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.GetThumbnail(ctx, hash, tt.size)

			require.NoError(t, err)
			assert.Equal(t, tt.size.Width, result.Width)
			assert.Equal(t, tt.size.Height, result.Height)
			assert.Equal(t, FormatJPEG, result.Format)
			assert.Equal(t, "image/jpeg", result.ContentType)
		})
	}
}

func TestMediaService_GetThumbnail_NotFound(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	_, err := service.GetThumbnail(ctx, "nonexistent", ThumbnailSmall)

	assert.Error(t, err)
}

func TestMediaService_GenerateAllThumbnails(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(1200, 900, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	results, err := service.GenerateAllThumbnails(ctx, hash)

	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Contains(t, results, "small")
	assert.Contains(t, results, "medium")
	assert.Contains(t, results, "large")

	// Verify each thumbnail has correct dimensions
	assert.Equal(t, 150, results["small"].Width)
	assert.Equal(t, 150, results["small"].Height)
	assert.Equal(t, 300, results["medium"].Width)
	assert.Equal(t, 300, results["medium"].Height)
	assert.Equal(t, 600, results["large"].Width)
	assert.Equal(t, 600, results["large"].Height)
}

func TestMediaService_GenerateAllThumbnails_NotFound(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	_, err := service.GenerateAllThumbnails(ctx, "nonexistent")

	assert.Error(t, err)
}

func TestMediaService_StoreThumbnails(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create test image data
	testData := createTestImage(1200, 900, "jpeg")

	hashes, err := service.StoreThumbnails(ctx, "original-hash", testData)

	require.NoError(t, err)
	assert.Len(t, hashes, 3)
	assert.Contains(t, hashes, "small")
	assert.Contains(t, hashes, "medium")
	assert.Contains(t, hashes, "large")

	// Verify thumbnails were stored in storage backend
	for size, hash := range hashes {
		exists, err := storage.Exists(ctx, hash)
		require.NoError(t, err, "thumbnail %s should exist", size)
		assert.True(t, exists, "thumbnail %s should exist in storage", size)

		// Verify stored data is valid
		data, err := storage.Size(ctx, hash)
		require.NoError(t, err)
		assert.Greater(t, data, int64(0), "thumbnail %s should have data", size)
	}
}

func TestMediaService_StoreThumbnails_InvalidImage(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Try with invalid image data
	invalidData := []byte("not an image")

	hashes, err := service.StoreThumbnails(ctx, "original-hash", invalidData)

	// Should not error, but should return empty hashes (failed thumbnails are skipped)
	require.NoError(t, err)
	assert.Len(t, hashes, 0)
}

func TestMediaService_Close(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)

	err := service.Close()

	assert.NoError(t, err)
}

func TestVariantKey(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)

	tests := []struct {
		name string
		hash string
		opts *ProcessOptions
		want string
	}{
		{
			name: "nil_options",
			hash: "abc123",
			opts: nil,
			want: "abc123",
		},
		{
			name: "with_dimensions",
			hash: "abc123",
			opts: &ProcessOptions{
				Width:  200,
				Height: 150,
			},
			want: "abc123_200x150__false",
		},
		{
			name: "with_format",
			hash: "abc123",
			opts: &ProcessOptions{
				Width:  200,
				Height: 150,
				Format: FormatPNG,
			},
			want: "abc123_200x150_png_false",
		},
		{
			name: "with_crop",
			hash: "abc123",
			opts: &ProcessOptions{
				Width:  200,
				Height: 150,
				Crop:   true,
			},
			want: "abc123_200x150__true",
		},
		{
			name: "full_options",
			hash: "def456",
			opts: &ProcessOptions{
				Width:  300,
				Height: 200,
				Format: FormatJPEG,
				Crop:   true,
			},
			want: "def456_300x200_jpeg_true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.variantKey(tt.hash, tt.opts)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMediaService_CacheExpiration(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 50*time.Millisecond) // Short TTL
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(400, 400, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	opts := &ProcessOptions{
		Width:  200,
		Height: 200,
	}

	// First request - should process
	result1, err := service.GetImage(ctx, hash, opts)
	require.NoError(t, err)

	// Immediately request again - should be cached
	cacheKey := service.variantKey(hash, opts)
	cached, ok := c.Get(ctx, cacheKey)
	assert.True(t, ok)
	assert.NotNil(t, cached)

	// Wait for cache expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired now
	cached, ok = c.Get(ctx, cacheKey)
	assert.False(t, ok)

	// Request again - should process again
	result2, err := service.GetImage(ctx, hash, opts)
	require.NoError(t, err)

	// Results should be functionally identical (data might differ slightly due to re-encoding)
	assert.Equal(t, result1.Width, result2.Width)
	assert.Equal(t, result1.Height, result2.Height)
}

func TestMediaService_ConcurrentAccess(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(10 * 1024 * 1024) // 10MB
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(800, 600, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	// Make concurrent requests
	done := make(chan bool)
	numRequests := 10

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			opts := &ProcessOptions{
				Width:  200,
				Height: 200,
			}
			_, err := service.GetImage(ctx, hash, opts)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < numRequests; i++ {
		<-done
	}
}

func TestMediaService_MultipleFormats(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	// Create and store a test image
	testData := createTestImage(400, 400, "jpeg")
	hash := hashData(testData)
	err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
	require.NoError(t, err)

	formats := []ImageFormat{FormatJPEG, FormatPNG, FormatGIF}

	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			result, err := service.GetImage(ctx, hash, &ProcessOptions{
				Width:  200,
				Height: 200,
				Format: format,
			})

			require.NoError(t, err)
			// WebP might fall back to JPEG
			if format == FormatWebP {
				assert.Contains(t, []ImageFormat{FormatJPEG, FormatWebP}, result.Format)
			} else {
				assert.Equal(t, format, result.Format)
			}
		})
	}
}

func TestMediaService_EdgeCases(t *testing.T) {
	storage := NewMockStorageBackend()
	c := cache.NewMemoryCache(1024 * 1024)
	service := NewMediaService(storage, c, 5*time.Minute)
	ctx := context.Background()

	t.Run("very_small_image", func(t *testing.T) {
		testData := createTestImage(1, 1, "jpeg")
		hash := hashData(testData)
		err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err)

		result, err := service.GetImage(ctx, hash, &ProcessOptions{
			Width:  10,
			Height: 10,
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("zero_width", func(t *testing.T) {
		testData := createTestImage(200, 200, "jpeg")
		hash := hashData(testData)
		err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err)

		result, err := service.GetImage(ctx, hash, &ProcessOptions{
			Width:  0,
			Height: 100,
		})

		require.NoError(t, err)
		assert.Equal(t, 100, result.Height)
		assert.Greater(t, result.Width, 0)
	})

	t.Run("zero_height", func(t *testing.T) {
		testData := createTestImage(200, 200, "jpeg")
		hash := hashData(testData)
		err := storage.Put(ctx, hash, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err)

		result, err := service.GetImage(ctx, hash, &ProcessOptions{
			Width:  100,
			Height: 0,
		})

		require.NoError(t, err)
		assert.Equal(t, 100, result.Width)
		assert.Greater(t, result.Height, 0)
	})
}
