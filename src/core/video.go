package core

import (
	"context"
	"errors"
)

var (
	// ErrVideoNotSupported is returned when a video format is not supported.
	ErrVideoNotSupported = errors.New("video format not supported")
	// ErrTranscodeInProgress is returned when transcoding is already in progress.
	ErrTranscodeInProgress = errors.New("transcoding in progress")
	// ErrTranscodeNotFound is returned when no transcoded version exists.
	ErrTranscodeNotFound = errors.New("transcoded version not found")
	// ErrFFmpegNotFound is returned when FFmpeg is not installed.
	ErrFFmpegNotFound = errors.New("ffmpeg not found")
)

// TranscodeStatus represents the status of a transcoding job.
type TranscodeStatus string

const (
	TranscodeStatusPending    TranscodeStatus = "pending"
	TranscodeStatusProcessing TranscodeStatus = "processing"
	TranscodeStatusComplete   TranscodeStatus = "complete"
	TranscodeStatusFailed     TranscodeStatus = "failed"
)

// VideoQuality represents a video quality preset.
type VideoQuality struct {
	Name       string // e.g., "720p", "480p", "360p"
	Width      int    // Target width
	Height     int    // Target height
	VideoBitrate int  // Video bitrate in kbps
	AudioBitrate int  // Audio bitrate in kbps
}

// Standard video quality presets.
var (
	Quality1080p = VideoQuality{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: 5000, AudioBitrate: 192}
	Quality720p  = VideoQuality{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 2500, AudioBitrate: 128}
	Quality480p  = VideoQuality{Name: "480p", Width: 854, Height: 480, VideoBitrate: 1000, AudioBitrate: 96}
	Quality360p  = VideoQuality{Name: "360p", Width: 640, Height: 360, VideoBitrate: 600, AudioBitrate: 96}

	DefaultQualities = []VideoQuality{Quality720p, Quality480p, Quality360p}
)

// TranscodeJob represents a video transcoding job.
type TranscodeJob struct {
	ID          string          // Job ID (usually the blob hash)
	BlobHash    string          // Original blob hash
	Status      TranscodeStatus // Current status
	Progress    int             // Progress percentage (0-100)
	Qualities   []VideoQuality  // Target qualities
	OutputDir   string          // Output directory for segments
	Error       string          // Error message if failed
	CreatedAt   int64           // Unix timestamp
	UpdatedAt   int64           // Unix timestamp
	CompletedAt int64           // Unix timestamp when completed
}

// HLSManifest represents an HLS manifest structure.
type HLSManifest struct {
	MasterPlaylist string            // Master .m3u8 content
	Variants       map[string]string // Quality -> variant .m3u8 content
}

// VideoService handles video transcoding and streaming.
type VideoService interface {
	// IsSupported returns true if the MIME type is a supported video format.
	IsSupported(mimeType string) bool

	// IsFFmpegAvailable checks if FFmpeg is installed and available.
	IsFFmpegAvailable() bool

	// StartTranscode starts a transcoding job for a blob.
	// Returns the job ID or error if transcoding cannot be started.
	StartTranscode(ctx context.Context, blobHash string, qualities []VideoQuality) (*TranscodeJob, error)

	// GetTranscodeStatus returns the current status of a transcoding job.
	GetTranscodeStatus(ctx context.Context, blobHash string) (*TranscodeJob, error)

	// GetHLSManifest returns the HLS manifest for a transcoded video.
	// Returns ErrTranscodeNotFound if not transcoded yet.
	GetHLSManifest(ctx context.Context, blobHash string) (*HLSManifest, error)

	// GetSegment returns a video segment file.
	GetSegment(ctx context.Context, blobHash, quality, segmentName string) ([]byte, error)

	// DeleteTranscodedFiles removes all transcoded files for a blob.
	DeleteTranscodedFiles(ctx context.Context, blobHash string) error
}
