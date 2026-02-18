package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrBlobNotFound is returned when a blob does not exist in storage.
	ErrBlobNotFound = errors.New("blob not found")

	// ErrPresignedURLNotSupported is returned when storage doesn't support presigned URLs.
	ErrPresignedURLNotSupported = errors.New("presigned URLs not supported by this storage backend")
)

// StorageBackend defines the interface for blob storage backends.
// Implementations handle the actual storage of blob bytes, while metadata
// (ownership, size, mime type) is stored separately in the database.
type StorageBackend interface {
	// Put stores blob data with the given hash as the key.
	// The hash should be the SHA-256 hash of the data.
	// If a blob with this hash already exists, it may be overwritten or the
	// operation may be a no-op depending on the implementation.
	Put(ctx context.Context, hash string, data io.Reader, size int64) error

	// Get retrieves the blob data for the given hash.
	// Returns ErrBlobNotFound if the blob does not exist.
	// The caller is responsible for closing the returned ReadCloser.
	Get(ctx context.Context, hash string) (io.ReadCloser, error)

	// Delete removes the blob with the given hash.
	// Returns nil if the blob does not exist (idempotent).
	Delete(ctx context.Context, hash string) error

	// Exists checks whether a blob with the given hash exists.
	Exists(ctx context.Context, hash string) (bool, error)

	// Size returns the size in bytes of the blob with the given hash.
	// Returns ErrBlobNotFound if the blob does not exist.
	Size(ctx context.Context, hash string) (int64, error)
}

// PresignedURLProvider is an optional interface that storage backends can implement
// to support direct access via presigned URLs (for CDN integration).
type PresignedURLProvider interface {
	// GetPresignedURL returns a presigned URL for direct access to the blob.
	// The URL is valid for the specified duration.
	// Returns ErrPresignedURLNotSupported if not implemented.
	GetPresignedURL(ctx context.Context, hash string, expiry time.Duration) (string, error)

	// GetPublicURL returns the public URL for a blob if the bucket is public.
	// Returns empty string if public access is not configured.
	GetPublicURL(hash string) string
}
