package core

import (
	"context"
	"errors"
	"time"
)

// Chunked upload errors
var (
	ErrSessionNotFound      = errors.New("upload session not found")
	ErrSessionExpired       = errors.New("upload session expired")
	ErrSessionAborted       = errors.New("upload session aborted")
	ErrSessionComplete      = errors.New("upload session already complete")
	ErrChunkAlreadyUploaded = errors.New("chunk already uploaded")
	ErrChunkOutOfRange      = errors.New("chunk number out of range")
	ErrChunkSizeMismatch    = errors.New("chunk size mismatch")
	ErrHashMismatch         = errors.New("final hash does not match expected")
	ErrInvalidChunkSize     = errors.New("invalid chunk size")
	ErrUploadIncomplete     = errors.New("not all chunks received")
)

// UploadSessionStatus represents the status of an upload session.
type UploadSessionStatus string

const (
	UploadSessionStatusActive   UploadSessionStatus = "active"
	UploadSessionStatusComplete UploadSessionStatus = "complete"
	UploadSessionStatusExpired  UploadSessionStatus = "expired"
	UploadSessionStatusAborted  UploadSessionStatus = "aborted"
)

// UploadSession represents an active chunked upload session.
type UploadSession struct {
	ID              string              `json:"id"`
	Pubkey          string              `json:"pubkey"`
	Hash            string              `json:"hash,omitempty"`       // Expected final hash (optional)
	TotalSize       int64               `json:"total_size"`           // Expected total size
	ChunkSize       int64               `json:"chunk_size"`           // Size of each chunk
	TotalChunks     int                 `json:"total_chunks"`         // Calculated total chunks
	MimeType        string              `json:"mime_type,omitempty"`  // Expected MIME type
	ChunksReceived  int                 `json:"chunks_received"`      // Number of chunks received
	BytesReceived   int64               `json:"bytes_received"`       // Total bytes received
	Status          UploadSessionStatus `json:"status"`               // Session status
	EncryptionMode  string              `json:"encryption_mode"`      // none, server, e2e
	Created         int64               `json:"created"`              // Unix timestamp
	Updated         int64               `json:"updated"`              // Last update timestamp
	ExpiresAt       int64               `json:"expires_at"`           // Expiration timestamp
}

// UploadChunk represents a single chunk within an upload session.
type UploadChunk struct {
	SessionID  string `json:"session_id"`
	ChunkNum   int    `json:"chunk_num"`   // 0-indexed
	Size       int64  `json:"size"`        // Chunk size
	Offset     int64  `json:"offset"`      // Byte offset in final file
	Hash       string `json:"hash"`        // SHA-256 of chunk
	ReceivedAt int64  `json:"received_at"` // Unix timestamp
}

// CreateSessionRequest contains parameters for creating a new upload session.
type CreateSessionRequest struct {
	Pubkey         string `json:"pubkey"`
	TotalSize      int64  `json:"total_size"`
	ChunkSize      int64  `json:"chunk_size,omitempty"` // Optional, server can set default
	MimeType       string `json:"mime_type,omitempty"`  // Optional
	Hash           string `json:"hash,omitempty"`       // Optional expected final hash
	EncryptionMode string `json:"encryption_mode,omitempty"`
	TTL            int64  `json:"ttl,omitempty"` // Session TTL in seconds
}

// CreateSessionResponse is returned when a new session is created.
type CreateSessionResponse struct {
	Session     *UploadSession `json:"session"`
	ChunkSize   int64          `json:"chunk_size"`   // Actual chunk size to use
	TotalChunks int            `json:"total_chunks"` // Number of chunks expected
	ExpiresAt   int64          `json:"expires_at"`   // Session expiration
	UploadURL   string         `json:"upload_url"`   // Base URL for chunk uploads
}

// UploadChunkRequest contains parameters for uploading a chunk.
type UploadChunkRequest struct {
	SessionID string `json:"session_id"`
	ChunkNum  int    `json:"chunk_num"` // 0-indexed
	Data      []byte `json:"-"`         // Chunk data (not serialized)
}

// CompleteUploadResponse is returned when an upload is finalized.
type CompleteUploadResponse struct {
	Blob *Blob  `json:"blob"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// ChunkedUploadService handles chunked file uploads.
type ChunkedUploadService interface {
	// CreateSession creates a new upload session.
	CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error)

	// UploadChunk uploads a single chunk to a session.
	UploadChunk(ctx context.Context, sessionID string, chunkNum int, data []byte) (*UploadChunk, error)

	// CompleteUpload finalizes the upload and creates the blob.
	CompleteUpload(ctx context.Context, sessionID string, expectedHash string) (*CompleteUploadResponse, error)

	// AbortUpload cancels an upload session and cleans up resources.
	AbortUpload(ctx context.Context, sessionID string) error

	// GetSession retrieves session information.
	GetSession(ctx context.Context, sessionID string) (*UploadSession, error)

	// GetChunks retrieves all chunks for a session.
	GetChunks(ctx context.Context, sessionID string) ([]UploadChunk, error)

	// CleanupExpiredSessions removes expired sessions and their data.
	CleanupExpiredSessions(ctx context.Context) (int, error)
}

// ChunkedUploadConfig contains configuration for chunked uploads.
type ChunkedUploadConfig struct {
	Enabled          bool          `yaml:"enabled"`
	DefaultChunkSize int64         `yaml:"default_chunk_size"` // Default chunk size in bytes (default: 5MB)
	MinChunkSize     int64         `yaml:"min_chunk_size"`     // Minimum allowed chunk size (default: 1MB)
	MaxChunkSize     int64         `yaml:"max_chunk_size"`     // Maximum allowed chunk size (default: 100MB)
	MaxSessionTTL    time.Duration `yaml:"max_session_ttl"`    // Maximum session lifetime (default: 24h)
	DefaultSessionTTL time.Duration `yaml:"default_session_ttl"` // Default session lifetime (default: 1h)
	TempDir          string        `yaml:"temp_dir"`           // Directory for temporary chunks
}

// DefaultChunkedUploadConfig returns sensible defaults.
func DefaultChunkedUploadConfig() ChunkedUploadConfig {
	return ChunkedUploadConfig{
		Enabled:          true,
		DefaultChunkSize: 5 * 1024 * 1024,   // 5MB
		MinChunkSize:     1 * 1024 * 1024,   // 1MB
		MaxChunkSize:     100 * 1024 * 1024, // 100MB
		MaxSessionTTL:    24 * time.Hour,
		DefaultSessionTTL: 1 * time.Hour,
		TempDir:          "/tmp/blossom-chunks",
	}
}
