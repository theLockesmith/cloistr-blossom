package core

import (
	"context"
	"io"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
)

type Services interface {
	Init(context.Context) error
	Blob() BlobStorage
	ACR() ACRStorage
	Mime() MimeTypeService
	Settings() SettingService
	Stats() StatService
	Quota() QuotaService
	Moderation() ModerationService
	Media() MediaService
	Video() VideoService
	CDN() CDNService
	Cache() cache.Cache
}

// CDNService handles content delivery network integration for blob serving.
type CDNService interface {
	// GetBlobURL returns the best URL for serving a blob.
	// If CDN is enabled and configured, returns a CDN/presigned URL.
	// Otherwise, falls back to the standard API URL.
	GetBlobURL(ctx context.Context, hash string, mimeType string) (string, error)

	// GetPresignedURL returns a presigned URL for direct access to storage.
	// Returns an error if presigned URLs are not supported or enabled.
	GetPresignedURL(ctx context.Context, hash string, expiry time.Duration) (string, error)

	// GetPublicURL returns the public CDN URL for a blob if available.
	// Returns empty string if public CDN is not configured.
	GetPublicURL(hash string) string

	// IsEnabled returns true if CDN delivery is enabled.
	IsEnabled() bool

	// ShouldRedirect returns true if requests should be redirected to CDN
	// rather than proxying the content.
	ShouldRedirect() bool
}

// MediaProcessOptions defines options for media processing.
type MediaProcessOptions struct {
	Width   int    // Target width (0 = preserve aspect ratio)
	Height  int    // Target height (0 = preserve aspect ratio)
	Quality int    // Output quality (1-100, 0 = default)
	Format  string // Output format (jpeg, png, webp)
}

// MediaProcessResult contains the result of media processing.
type MediaProcessResult struct {
	Data        []byte
	Width       int
	Height      int
	ContentType string
	Hash        string // SHA256 of the processed data
}

// MediaService handles media processing and optimization.
type MediaService interface {
	// ProcessImage processes an image according to the given options.
	ProcessImage(ctx context.Context, data io.Reader, mimeType string, opts *MediaProcessOptions) (*MediaProcessResult, error)
	// IsSupported returns true if the given MIME type can be processed.
	IsSupported(mimeType string) bool
	// GetThumbnail retrieves or generates a thumbnail for a blob.
	GetThumbnail(ctx context.Context, hash string, width, height int) (*MediaProcessResult, error)
}
