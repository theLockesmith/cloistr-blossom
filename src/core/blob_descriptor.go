package core

import (
	"context"
	"errors"
)

var (
	ErrBlobNotFound = errors.New("blob not found")
)

// BlobFilter defines filtering options for listing blobs.
type BlobFilter struct {
	// TypePrefix filters by MIME type prefix (e.g., "image/" for all images)
	TypePrefix string
	// Since filters blobs created after this Unix timestamp
	Since int64
	// Until filters blobs created before this Unix timestamp
	Until int64
	// Limit is the maximum number of results to return (0 = no limit)
	Limit int
	// Offset is the number of results to skip for pagination
	Offset int
	// SortDesc sorts by created timestamp descending (newest first) if true
	SortDesc bool
}

// BlobListResult contains the results of a filtered blob listing.
type BlobListResult struct {
	Blobs []*Blob
	Total int64 // Total count without pagination
}

// EncryptionMode represents the encryption state of a blob.
type EncryptionMode string

const (
	// EncryptionModeNone indicates plaintext storage (no encryption).
	EncryptionModeNone EncryptionMode = "none"
	// EncryptionModeServer indicates server-side encryption at rest.
	EncryptionModeServer EncryptionMode = "server"
	// EncryptionModeE2E indicates end-to-end encryption (client-encrypted, server cannot decrypt).
	EncryptionModeE2E EncryptionMode = "e2e"
)

type Blob struct {
	Pubkey         string
	Url            string
	Sha256         string
	Size           int64
	Type           string
	Blob           []byte
	Uploaded       int64
	NIP94          *NIP94FileMetadata
	EncryptionMode EncryptionMode // Encryption mode for this blob
}

type NIP94FileMetadata struct {
	Url            string
	MimeType       string
	Sha256         string
	OriginalSha256 string
	Size           *int64
	Dimension      *string
	Magnet         *string
	Infohash       *string
	Blurhash       *string
	ThumbnailUrl   *string
	ImageUrl       *string
	Summary        *string
	Alt            *string
	Fallback       *string
	Service        *string
}

type BlobStorage interface {
	Save(
		ctx context.Context,
		pubkey string,
		sha256 string,
		url string,
		size int64,
		mimeType string,
		blob []byte,
		created int64,
		encryptionMode EncryptionMode, // "none", "server", or "e2e"
	) (*Blob, error)
	Exists(ctx context.Context, sha256 string) (bool, error)
	GetFromHash(ctx context.Context, sha256 string) (*Blob, error)
	GetFromPubkey(ctx context.Context, pubkey string) ([]*Blob, error)
	// GetFromPubkeyWithFilter returns blobs for a pubkey with filtering and pagination.
	GetFromPubkeyWithFilter(ctx context.Context, pubkey string, filter *BlobFilter) (*BlobListResult, error)
	DeleteFromHash(ctx context.Context, sha256 string) error
	// IsEncryptionEnabled returns true if server-side encryption is available.
	IsEncryptionEnabled() bool

	// Deduplication methods

	// SaveWithDedup saves a blob with content-addressable deduplication.
	// If the blob already exists (same hash), it creates a reference for this user
	// without re-storing the data. Returns (blob, isNewBlob, error).
	SaveWithDedup(
		ctx context.Context,
		pubkey string,
		sha256 string,
		url string,
		size int64,
		mimeType string,
		blob []byte,
		created int64,
		encryptionMode EncryptionMode,
	) (*Blob, bool, error)

	// HasReference checks if a user has a reference to a blob.
	HasReference(ctx context.Context, pubkey string, sha256 string) (bool, error)

	// DeleteReference removes a user's reference to a blob.
	// If this was the last reference, the actual blob is deleted from storage.
	// Returns (wasLastReference, error).
	DeleteReference(ctx context.Context, pubkey string, sha256 string) (bool, error)
}
