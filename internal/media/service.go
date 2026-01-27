package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/storage"
)

// MediaService handles media processing and caching.
type MediaService struct {
	storage   storage.StorageBackend
	processor *ImageProcessor
	cache     *variantCache
}

// variantCache caches processed image variants.
type variantCache struct {
	mu    sync.RWMutex
	items map[string][]byte
	// maxSize is the maximum cache size in bytes
	maxSize int64
	// currentSize is the current cache size
	currentSize int64
}

func newVariantCache(maxSize int64) *variantCache {
	return &variantCache{
		items:   make(map[string][]byte),
		maxSize: maxSize,
	}
}

func (c *variantCache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, ok := c.items[key]
	return data, ok
}

func (c *variantCache) set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction: if adding this would exceed max, clear cache
	newSize := int64(len(data))
	if c.currentSize+newSize > c.maxSize {
		c.items = make(map[string][]byte)
		c.currentSize = 0
	}

	c.items[key] = data
	c.currentSize += newSize
}

// NewMediaService creates a new media service.
func NewMediaService(storage storage.StorageBackend) *MediaService {
	return &MediaService{
		storage:   storage,
		processor: NewImageProcessor(),
		cache:     newVariantCache(100 * 1024 * 1024), // 100MB cache
	}
}

// GetImage retrieves an image, optionally processing it.
func (s *MediaService) GetImage(ctx context.Context, hash string, opts *ProcessOptions) (*ProcessResult, error) {
	// Generate cache key
	cacheKey := s.variantKey(hash, opts)

	// Check cache first
	if cached, ok := s.cache.get(cacheKey); ok {
		// Parse format from options to return correct content type
		format := FormatJPEG
		if opts != nil && opts.Format != "" {
			format = opts.Format
		}
		return &ProcessResult{
			Data:        cached,
			Format:      format,
			ContentType: formatToContentType(format),
		}, nil
	}

	// Get original from storage
	reader, err := s.storage.Get(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("get image from storage: %w", err)
	}
	defer reader.Close()

	// If no processing needed, return original
	if opts == nil || (opts.Width == 0 && opts.Height == 0 && opts.Format == "") {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read image data: %w", err)
		}
		return &ProcessResult{
			Data:        data,
			ContentType: "application/octet-stream",
		}, nil
	}

	// Process the image
	result, err := s.processor.Process(reader, *opts)
	if err != nil {
		return nil, fmt.Errorf("process image: %w", err)
	}

	// Cache the result
	s.cache.set(cacheKey, result.Data)

	return result, nil
}

// GetThumbnail retrieves or generates a thumbnail.
func (s *MediaService) GetThumbnail(ctx context.Context, hash string, size ThumbnailSize) (*ProcessResult, error) {
	return s.GetImage(ctx, hash, &ProcessOptions{
		Width:  size.Width,
		Height: size.Height,
		Crop:   true,
		Format: FormatJPEG,
	})
}

// GenerateAllThumbnails generates all standard thumbnail sizes for an image.
func (s *MediaService) GenerateAllThumbnails(ctx context.Context, hash string) (map[string]*ProcessResult, error) {
	results := make(map[string]*ProcessResult)

	for _, size := range DefaultThumbnailSizes {
		result, err := s.GetThumbnail(ctx, hash, size)
		if err != nil {
			return nil, fmt.Errorf("generate thumbnail %s: %w", size.Name, err)
		}
		results[size.Name] = result
	}

	return results, nil
}

// StoreThumbnails generates and stores thumbnails in the storage backend.
func (s *MediaService) StoreThumbnails(ctx context.Context, hash string, data []byte) (map[string]string, error) {
	hashes := make(map[string]string)

	for _, size := range DefaultThumbnailSizes {
		reader := bytes.NewReader(data)
		result, err := s.processor.GenerateThumbnail(reader, size)
		if err != nil {
			continue // Skip failed thumbnails
		}

		// Generate hash for the thumbnail
		thumbHash := sha256.Sum256(result.Data)
		thumbHashStr := hex.EncodeToString(thumbHash[:])

		// Store in storage backend
		thumbReader := bytes.NewReader(result.Data)
		if err := s.storage.Put(ctx, thumbHashStr, thumbReader, int64(len(result.Data))); err != nil {
			continue // Skip failed storage
		}

		hashes[size.Name] = thumbHashStr
	}

	return hashes, nil
}

// variantKey generates a cache key for a processed variant.
func (s *MediaService) variantKey(hash string, opts *ProcessOptions) string {
	if opts == nil {
		return hash
	}
	return fmt.Sprintf("%s_%dx%d_%s_%v", hash, opts.Width, opts.Height, opts.Format, opts.Crop)
}
