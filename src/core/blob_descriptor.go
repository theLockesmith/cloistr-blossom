package core

import (
	"context"
	"errors"
)

var (
	ErrBlobNotFound = errors.New("blob not found")
)

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
	DeleteFromHash(ctx context.Context, sha256 string) error
	// IsEncryptionEnabled returns true if server-side encryption is available.
	IsEncryptionEnabled() bool
}
