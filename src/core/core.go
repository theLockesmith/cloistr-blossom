package core

import (
	"context"
	"io"

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
	Cache() cache.Cache
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
