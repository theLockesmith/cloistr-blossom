package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/media"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"go.uber.org/zap"
)

type mediaService struct {
	storage   storage.StorageBackend
	processor *media.ImageProcessor
	cache     cache.Cache
	cacheTTL  time.Duration
	log       *zap.Logger
}

// MediaConfig holds configuration for the media service.
type MediaConfig struct {
	CacheTTL    time.Duration
	JPEGQuality int
	WebPQuality int
}

// DefaultMediaConfig returns a default media configuration.
func DefaultMediaConfig() MediaConfig {
	return MediaConfig{
		CacheTTL:    1 * time.Hour,
		JPEGQuality: 85,
		WebPQuality: 80,
	}
}

// NewMediaService creates a new media service.
func NewMediaService(
	storageBackend storage.StorageBackend,
	appCache cache.Cache,
	conf MediaConfig,
	log *zap.Logger,
) (core.MediaService, error) {
	processor := media.NewImageProcessor()
	processor.JPEGQuality = conf.JPEGQuality
	processor.WebPQuality = conf.WebPQuality

	return &mediaService{
		storage:   storageBackend,
		processor: processor,
		cache:     appCache,
		cacheTTL:  conf.CacheTTL,
		log:       log,
	}, nil
}

// ProcessImage processes an image according to the given options.
func (s *mediaService) ProcessImage(ctx context.Context, data io.Reader, mimeType string, opts *core.MediaProcessOptions) (*core.MediaProcessResult, error) {
	// Read all data into memory for processing
	imageData, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("read image data: %w", err)
	}

	// Build processing options
	procOpts := media.ProcessOptions{}
	if opts != nil {
		procOpts.Width = opts.Width
		procOpts.Height = opts.Height
		procOpts.Quality = opts.Quality
		if opts.Format != "" {
			procOpts.Format = media.ImageFormat(opts.Format)
		}
	}

	// Default optimization for images without explicit options
	if opts == nil || (opts.Width == 0 && opts.Height == 0) {
		// Apply default optimization: convert to JPEG with quality settings
		procOpts.Format = media.FormatJPEG
		procOpts.Quality = s.processor.JPEGQuality
	}

	// Process the image
	result, err := s.processor.Process(bytes.NewReader(imageData), procOpts)
	if err != nil {
		return nil, fmt.Errorf("process image: %w", err)
	}

	// Calculate hash of processed data
	hash := sha256.Sum256(result.Data)
	hashStr := hex.EncodeToString(hash[:])

	return &core.MediaProcessResult{
		Data:        result.Data,
		Width:       result.Width,
		Height:      result.Height,
		ContentType: result.ContentType,
		Hash:        hashStr,
	}, nil
}

// IsSupported returns true if the given MIME type can be processed.
func (s *mediaService) IsSupported(mimeType string) bool {
	return media.IsImage(mimeType)
}

// GetThumbnail retrieves or generates a thumbnail for a blob.
func (s *mediaService) GetThumbnail(ctx context.Context, hash string, width, height int) (*core.MediaProcessResult, error) {
	// Generate cache key
	cacheKey := fmt.Sprintf("thumb_%s_%dx%d", hash, width, height)

	// Check cache first
	if s.cache != nil {
		if cached, ok := s.cache.Get(ctx, cacheKey); ok {
			// Parse cached thumbnail
			thumbHash := sha256.Sum256(cached)
			return &core.MediaProcessResult{
				Data:        cached,
				Width:       width,
				Height:      height,
				ContentType: "image/jpeg",
				Hash:        hex.EncodeToString(thumbHash[:]),
			}, nil
		}
	}

	// Get original from storage
	reader, err := s.storage.Get(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("get blob from storage: %w", err)
	}
	defer reader.Close()

	// Generate thumbnail
	result, err := s.processor.Process(reader, media.ProcessOptions{
		Width:  width,
		Height: height,
		Crop:   true,
		Format: media.FormatJPEG,
	})
	if err != nil {
		return nil, fmt.Errorf("generate thumbnail: %w", err)
	}

	// Calculate hash
	thumbHash := sha256.Sum256(result.Data)
	hashStr := hex.EncodeToString(thumbHash[:])

	// Cache the result
	if s.cache != nil {
		s.cache.Set(ctx, cacheKey, result.Data, s.cacheTTL)
	}

	return &core.MediaProcessResult{
		Data:        result.Data,
		Width:       result.Width,
		Height:      result.Height,
		ContentType: result.ContentType,
		Hash:        hashStr,
	}, nil
}
