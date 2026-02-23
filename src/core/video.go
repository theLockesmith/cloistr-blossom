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
	// ErrSubtitleNotFound is returned when a subtitle track is not found.
	ErrSubtitleNotFound = errors.New("subtitle not found")
	// ErrInvalidSubtitleFormat is returned when subtitle format is invalid.
	ErrInvalidSubtitleFormat = errors.New("invalid subtitle format (must be WebVTT)")
)

// HWAccelType represents a hardware acceleration type.
type HWAccelType string

const (
	// HWAccelNone disables hardware acceleration (software encoding).
	HWAccelNone HWAccelType = "none"
	// HWAccelNVENC uses NVIDIA GPU encoding via NVENC.
	HWAccelNVENC HWAccelType = "nvenc"
	// HWAccelQSV uses Intel Quick Sync Video.
	HWAccelQSV HWAccelType = "qsv"
	// HWAccelVAAPI uses Video Acceleration API (AMD/Intel on Linux).
	HWAccelVAAPI HWAccelType = "vaapi"
	// HWAccelAuto automatically detects the best available encoder.
	HWAccelAuto HWAccelType = "auto"
)

// VideoCodec represents a video codec for transcoding.
type VideoCodec string

const (
	// CodecH264 uses H.264/AVC encoding (best compatibility).
	CodecH264 VideoCodec = "h264"
	// CodecHEVC uses H.265/HEVC encoding (better compression, good device support).
	CodecHEVC VideoCodec = "hevc"
	// CodecAV1 uses AV1 encoding (best compression, newer device support).
	CodecAV1 VideoCodec = "av1"
)

// HWAccelConfig holds hardware acceleration configuration.
type HWAccelConfig struct {
	Type      HWAccelType // Type of hardware acceleration to use
	Codec     VideoCodec  // Video codec to use (h264, hevc, av1)
	Device    string      // Device path for VAAPI (e.g., /dev/dri/renderD128)
	Preset    string      // Encoder preset (varies by encoder)
	LookAhead int         // Look-ahead frames for NVENC (0 = disabled)
}

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

// Standard video quality presets for H.264.
var (
	Quality1080p = VideoQuality{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: 5000, AudioBitrate: 192}
	Quality720p  = VideoQuality{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 2500, AudioBitrate: 128}
	Quality480p  = VideoQuality{Name: "480p", Width: 854, Height: 480, VideoBitrate: 1000, AudioBitrate: 96}
	Quality360p  = VideoQuality{Name: "360p", Width: 640, Height: 360, VideoBitrate: 600, AudioBitrate: 96}

	DefaultQualities = []VideoQuality{Quality720p, Quality480p, Quality360p}
)

// HEVC (H.265) quality presets - ~30% more efficient than H.264.
var (
	HEVCQuality1080p = VideoQuality{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: 3500, AudioBitrate: 192}
	HEVCQuality720p  = VideoQuality{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 1750, AudioBitrate: 128}
	HEVCQuality480p  = VideoQuality{Name: "480p", Width: 854, Height: 480, VideoBitrate: 700, AudioBitrate: 96}
	HEVCQuality360p  = VideoQuality{Name: "360p", Width: 640, Height: 360, VideoBitrate: 420, AudioBitrate: 96}

	HEVCDefaultQualities = []VideoQuality{HEVCQuality720p, HEVCQuality480p, HEVCQuality360p}
)

// AV1 quality presets - ~40% more efficient than H.264.
var (
	AV1Quality1080p = VideoQuality{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: 3000, AudioBitrate: 192}
	AV1Quality720p  = VideoQuality{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 1500, AudioBitrate: 128}
	AV1Quality480p  = VideoQuality{Name: "480p", Width: 854, Height: 480, VideoBitrate: 600, AudioBitrate: 96}
	AV1Quality360p  = VideoQuality{Name: "360p", Width: 640, Height: 360, VideoBitrate: 360, AudioBitrate: 96}

	AV1DefaultQualities = []VideoQuality{AV1Quality720p, AV1Quality480p, AV1Quality360p}
)

// GetDefaultQualities returns the default quality presets for a given codec.
func GetDefaultQualities(codec VideoCodec) []VideoQuality {
	switch codec {
	case CodecHEVC:
		return HEVCDefaultQualities
	case CodecAV1:
		return AV1DefaultQualities
	default:
		return DefaultQualities
	}
}

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

// DASHManifest represents a DASH manifest structure.
type DASHManifest struct {
	MPD string // Media Presentation Description (.mpd) content
}

// Subtitle represents a subtitle track for a video.
type Subtitle struct {
	Language string `json:"language"` // ISO 639-1 language code (e.g., "en", "es", "fr")
	Label    string `json:"label"`    // Human-readable label (e.g., "English", "Spanish")
	Default  bool   `json:"default"`  // Whether this is the default subtitle track
	Forced   bool   `json:"forced"`   // Whether subtitles are forced (for foreign language parts)
}

// SubtitleTrack represents a stored subtitle track with its content.
type SubtitleTrack struct {
	Subtitle
	BlobHash  string `json:"blob_hash"`  // Associated video blob hash
	CreatedAt int64  `json:"created_at"` // Unix timestamp
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

	// GetDASHManifest returns the DASH manifest for a transcoded video.
	// Returns ErrTranscodeNotFound if not transcoded yet.
	GetDASHManifest(ctx context.Context, blobHash string) (*DASHManifest, error)

	// GetSegment returns a video segment file (works for both HLS .ts and DASH .m4s).
	GetSegment(ctx context.Context, blobHash, quality, segmentName string) ([]byte, error)

	// GetDASHSegment returns a DASH segment file (.m4s or init.mp4).
	GetDASHSegment(ctx context.Context, blobHash, segmentName string) ([]byte, error)

	// DeleteTranscodedFiles removes all transcoded files for a blob.
	DeleteTranscodedFiles(ctx context.Context, blobHash string) error

	// AddSubtitle adds a subtitle track to a video.
	// The content must be valid WebVTT format.
	AddSubtitle(ctx context.Context, blobHash string, subtitle Subtitle, content []byte) error

	// GetSubtitle retrieves a subtitle track for a video.
	GetSubtitle(ctx context.Context, blobHash, language string) ([]byte, error)

	// ListSubtitles returns all subtitle tracks for a video.
	ListSubtitles(ctx context.Context, blobHash string) ([]SubtitleTrack, error)

	// DeleteSubtitle removes a subtitle track from a video.
	DeleteSubtitle(ctx context.Context, blobHash, language string) error
}
