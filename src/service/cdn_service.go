package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

type cdnService struct {
	storage       storage.StorageBackend
	conf          *config.CDNConfig
	cdnBaseURL    string // Legacy CdnUrl from main config
	presignExpiry time.Duration
	log           *zap.Logger
}

// CDNServiceConfig holds configuration for the CDN service.
type CDNServiceConfig struct {
	CDNConfig  *config.CDNConfig
	CDNBaseURL string // Legacy CdnUrl for fallback
}

// NewCDNService creates a new CDN service.
func NewCDNService(
	storageBackend storage.StorageBackend,
	conf CDNServiceConfig,
	log *zap.Logger,
) (core.CDNService, error) {
	// Parse presigned URL expiry duration
	expiry := time.Hour // default
	if conf.CDNConfig != nil && conf.CDNConfig.PresignedExpiry != "" {
		parsed, err := time.ParseDuration(conf.CDNConfig.PresignedExpiry)
		if err != nil {
			return nil, fmt.Errorf("invalid presigned_expiry duration: %w", err)
		}
		expiry = parsed
	}

	cdnConf := conf.CDNConfig
	if cdnConf == nil {
		cdnConf = &config.CDNConfig{}
	}

	return &cdnService{
		storage:       storageBackend,
		conf:          cdnConf,
		cdnBaseURL:    conf.CDNBaseURL,
		presignExpiry: expiry,
		log:           log,
	}, nil
}

// GetBlobURL returns the best URL for serving a blob.
func (s *cdnService) GetBlobURL(ctx context.Context, hash string, mimeType string) (string, error) {
	// If CDN is not enabled, return the standard API URL
	if !s.IsEnabled() {
		return fmt.Sprintf("%s/%s", s.cdnBaseURL, hash), nil
	}

	// Try public URL first (most efficient - no signing needed)
	if s.conf.PublicURL != "" {
		return s.GetPublicURL(hash), nil
	}

	// Try presigned URL if enabled
	if s.conf.PresignedURLs {
		url, err := s.GetPresignedURL(ctx, hash, s.presignExpiry)
		if err == nil {
			return url, nil
		}
		s.log.Warn("failed to generate presigned URL, falling back to API URL",
			zap.String("hash", hash),
			zap.Error(err))
	}

	// Fall back to standard API URL
	return fmt.Sprintf("%s/%s", s.cdnBaseURL, hash), nil
}

// GetPresignedURL returns a presigned URL for direct access to storage.
func (s *cdnService) GetPresignedURL(ctx context.Context, hash string, expiry time.Duration) (string, error) {
	// Check if storage supports presigned URLs
	provider, ok := s.storage.(storage.PresignedURLProvider)
	if !ok {
		return "", storage.ErrPresignedURLNotSupported
	}

	return provider.GetPresignedURL(ctx, hash, expiry)
}

// GetPublicURL returns the public CDN URL for a blob.
func (s *cdnService) GetPublicURL(hash string) string {
	// First check if storage has a public URL configured
	if provider, ok := s.storage.(storage.PresignedURLProvider); ok {
		if url := provider.GetPublicURL(hash); url != "" {
			return url
		}
	}

	// Fall back to CDN public URL
	if s.conf.PublicURL != "" {
		return fmt.Sprintf("%s/%s", s.conf.PublicURL, hash)
	}

	return ""
}

// IsEnabled returns true if CDN delivery is enabled.
func (s *cdnService) IsEnabled() bool {
	return s.conf.Enabled
}

// ShouldRedirect returns true if requests should be redirected to CDN.
func (s *cdnService) ShouldRedirect() bool {
	return s.conf.Enabled && s.conf.Redirect
}

// CacheControl returns the Cache-Control header value for CDN-served content.
func (s *cdnService) CacheControl() string {
	if s.conf.CacheControl != "" {
		return s.conf.CacheControl
	}
	return "public, max-age=31536000"
}
