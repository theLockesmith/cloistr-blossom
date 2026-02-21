package core

import (
	"context"
	"errors"
)

var (
	// ErrTorrentNotFound is returned when a torrent file is not found.
	ErrTorrentNotFound = errors.New("torrent not found")
	// ErrTorrentGenerationFailed is returned when torrent generation fails.
	ErrTorrentGenerationFailed = errors.New("torrent generation failed")
)

// TorrentInfo contains metadata about a generated torrent.
type TorrentInfo struct {
	BlobHash    string `json:"blob_hash"`    // Original blob hash
	InfoHash    string `json:"info_hash"`    // BitTorrent info hash (hex)
	MagnetURI   string `json:"magnet_uri"`   // Magnet link
	PieceLength int64  `json:"piece_length"` // Piece size in bytes
	PieceCount  int    `json:"piece_count"`  // Number of pieces
	TotalSize   int64  `json:"total_size"`   // Total file size
	Name        string `json:"name"`         // Torrent name
	CreatedAt   int64  `json:"created_at"`   // Unix timestamp
}

// TorrentConfig holds configuration for torrent generation.
type TorrentConfig struct {
	// WebSeedURLs are HTTP URLs where the blob can be downloaded (BEP 19)
	WebSeedURLs []string

	// TrackerURLs are BitTorrent tracker announce URLs
	TrackerURLs []string

	// EnableDHT enables DHT bootstrap nodes for tracker-less operation
	EnableDHT bool

	// Comment is an optional comment in the torrent file
	Comment string

	// CreatedBy identifies the software that created the torrent
	CreatedBy string
}

// TorrentService handles torrent file generation for blobs.
type TorrentService interface {
	// GenerateTorrent creates a .torrent file for a blob.
	// Returns the torrent info and the raw torrent file bytes.
	GenerateTorrent(ctx context.Context, blobHash string, config *TorrentConfig) (*TorrentInfo, []byte, error)

	// GetTorrent retrieves a previously generated torrent file.
	// Returns ErrTorrentNotFound if not cached.
	GetTorrent(ctx context.Context, blobHash string) ([]byte, error)

	// GetTorrentInfo retrieves metadata about a generated torrent.
	GetTorrentInfo(ctx context.Context, blobHash string) (*TorrentInfo, error)

	// DeleteTorrent removes a cached torrent file.
	DeleteTorrent(ctx context.Context, blobHash string) error
}
