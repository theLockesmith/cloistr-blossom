package core

import (
	"context"
	"errors"
)

var (
	// ErrIPFSNotConfigured is returned when IPFS pinning is not configured.
	ErrIPFSNotConfigured = errors.New("IPFS pinning not configured")
	// ErrIPFSPinNotFound is returned when a pin is not found.
	ErrIPFSPinNotFound = errors.New("IPFS pin not found")
	// ErrIPFSPinFailed is returned when pinning fails.
	ErrIPFSPinFailed = errors.New("IPFS pinning failed")
)

// IPFSPinStatus represents the status of an IPFS pin.
type IPFSPinStatus string

const (
	IPFSPinStatusQueued  IPFSPinStatus = "queued"
	IPFSPinStatusPinning IPFSPinStatus = "pinning"
	IPFSPinStatusPinned  IPFSPinStatus = "pinned"
	IPFSPinStatusFailed  IPFSPinStatus = "failed"
)

// IPFSPin represents a pinned blob on IPFS.
type IPFSPin struct {
	BlobHash  string            `json:"blob_hash"`  // Original blob hash (SHA-256)
	CID       string            `json:"cid"`        // IPFS Content Identifier
	Name      string            `json:"name"`       // Optional name for the pin
	Status    IPFSPinStatus     `json:"status"`     // Current pin status
	RequestID string            `json:"request_id"` // Pinning service request ID
	Meta      map[string]string `json:"meta"`       // Additional metadata
	CreatedAt int64             `json:"created_at"` // Unix timestamp
	PinnedAt  int64             `json:"pinned_at"`  // Unix timestamp when pinned
}

// IPFSService handles IPFS pinning operations.
type IPFSService interface {
	// IsConfigured returns true if IPFS pinning is configured.
	IsConfigured() bool

	// PinBlob pins a blob to IPFS via a pinning service.
	// Returns the IPFS CID and pin status.
	PinBlob(ctx context.Context, blobHash string, name string) (*IPFSPin, error)

	// UnpinBlob removes a blob from IPFS pinning.
	UnpinBlob(ctx context.Context, blobHash string) error

	// GetPinStatus returns the current pin status for a blob.
	GetPinStatus(ctx context.Context, blobHash string) (*IPFSPin, error)

	// ListPins returns all pins for the configured service.
	ListPins(ctx context.Context, status IPFSPinStatus, limit int) ([]IPFSPin, error)

	// GetIPFSGatewayURL returns the gateway URL for accessing a pinned blob.
	GetIPFSGatewayURL(cid string) string
}
