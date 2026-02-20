package service

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

func TestVideoServiceIsSupported(t *testing.T) {
	// Create a minimal video service for testing
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Test supported formats
	assert.True(t, svc.IsSupported("video/mp4"), "MP4 should be supported")
	assert.True(t, svc.IsSupported("video/webm"), "WebM should be supported")
	assert.True(t, svc.IsSupported("video/quicktime"), "QuickTime should be supported")
	assert.True(t, svc.IsSupported("video/x-matroska"), "MKV should be supported")

	// Test unsupported formats
	assert.False(t, svc.IsSupported("image/png"), "PNG should not be supported")
	assert.False(t, svc.IsSupported("audio/mp3"), "MP3 should not be supported")
	assert.False(t, svc.IsSupported("application/pdf"), "PDF should not be supported")
}

func TestVideoServiceFFmpegAvailability(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Check if FFmpeg is available
	ffmpegAvailable := svc.IsFFmpegAvailable()

	// This test just verifies the check works - result depends on system
	_, lookupErr := exec.LookPath("ffmpeg")
	expectedAvailable := lookupErr == nil

	assert.Equal(t, expectedAvailable, ffmpegAvailable, "FFmpeg availability should match system state")

	if ffmpegAvailable {
		t.Log("FFmpeg is available on this system")
	} else {
		t.Log("FFmpeg is NOT available on this system")
	}
}

func TestVideoServiceTranscodeStatusNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Try to get status of non-existent job
	_, err = svc.GetTranscodeStatus(context.Background(), "nonexistent-hash")
	assert.ErrorIs(t, err, core.ErrTranscodeNotFound)
}

func TestVideoServiceManifestNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Try to get manifest of non-transcoded video
	_, err = svc.GetHLSManifest(context.Background(), "nonexistent-hash")
	assert.ErrorIs(t, err, core.ErrTranscodeNotFound)
}

func TestVideoQualityPresets(t *testing.T) {
	// Test that quality presets are defined correctly
	assert.Equal(t, "1080p", core.Quality1080p.Name)
	assert.Equal(t, 1920, core.Quality1080p.Width)
	assert.Equal(t, 1080, core.Quality1080p.Height)

	assert.Equal(t, "720p", core.Quality720p.Name)
	assert.Equal(t, 1280, core.Quality720p.Width)
	assert.Equal(t, 720, core.Quality720p.Height)

	assert.Equal(t, "480p", core.Quality480p.Name)
	assert.Equal(t, 854, core.Quality480p.Width)
	assert.Equal(t, 480, core.Quality480p.Height)

	assert.Equal(t, "360p", core.Quality360p.Name)
	assert.Equal(t, 640, core.Quality360p.Width)
	assert.Equal(t, 360, core.Quality360p.Height)

	// Test default qualities
	assert.Len(t, core.DefaultQualities, 3, "should have 3 default qualities")
}

func TestTranscodeStatusValues(t *testing.T) {
	// Test status constants
	assert.Equal(t, core.TranscodeStatus("pending"), core.TranscodeStatusPending)
	assert.Equal(t, core.TranscodeStatus("processing"), core.TranscodeStatusProcessing)
	assert.Equal(t, core.TranscodeStatus("complete"), core.TranscodeStatusComplete)
	assert.Equal(t, core.TranscodeStatus("failed"), core.TranscodeStatusFailed)
}

func TestVideoServiceDASHManifestNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Try to get DASH manifest of non-transcoded video
	_, err = svc.GetDASHManifest(context.Background(), "nonexistent-hash")
	assert.ErrorIs(t, err, core.ErrTranscodeNotFound)
}

func TestVideoServiceDASHSegmentNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Try to get DASH segment of non-transcoded video
	_, err = svc.GetDASHSegment(context.Background(), "nonexistent-hash", "init-stream0.m4s")
	assert.Error(t, err)
}

func TestDASHManifestType(t *testing.T) {
	// Test that DASHManifest type is defined correctly
	manifest := core.DASHManifest{
		MPD: "<?xml version=\"1.0\"?><MPD></MPD>",
	}
	assert.NotEmpty(t, manifest.MPD)
}

func TestVideoServiceAddSubtitleValid(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Valid WebVTT content
	vttContent := []byte(`WEBVTT

00:00:00.000 --> 00:00:05.000
Hello, world!

00:00:05.000 --> 00:00:10.000
This is a test subtitle.
`)

	subtitle := core.Subtitle{
		Language: "en",
		Label:    "English",
		Default:  true,
		Forced:   false,
	}

	err = svc.AddSubtitle(context.Background(), "test-hash", subtitle, vttContent)
	assert.NoError(t, err)

	// Verify subtitle was stored
	content, err := svc.GetSubtitle(context.Background(), "test-hash", "en")
	assert.NoError(t, err)
	assert.Equal(t, vttContent, content)
}

func TestVideoServiceAddSubtitleInvalid(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Invalid content (not WebVTT)
	invalidContent := []byte(`This is not a valid WebVTT file.
It does not start with WEBVTT.
`)

	subtitle := core.Subtitle{
		Language: "en",
		Label:    "English",
	}

	err = svc.AddSubtitle(context.Background(), "test-hash", subtitle, invalidContent)
	assert.ErrorIs(t, err, core.ErrInvalidSubtitleFormat)
}

func TestVideoServiceGetSubtitleNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// Try to get non-existent subtitle
	_, err = svc.GetSubtitle(context.Background(), "nonexistent-hash", "en")
	assert.ErrorIs(t, err, core.ErrSubtitleNotFound)
}

func TestVideoServiceListSubtitles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	vttContent := []byte(`WEBVTT

00:00:00.000 --> 00:00:05.000
Test subtitle.
`)

	// Add multiple subtitles
	err = svc.AddSubtitle(context.Background(), "test-hash", core.Subtitle{
		Language: "en",
		Label:    "English",
		Default:  true,
	}, vttContent)
	require.NoError(t, err)

	err = svc.AddSubtitle(context.Background(), "test-hash", core.Subtitle{
		Language: "es",
		Label:    "Spanish",
		Default:  false,
	}, vttContent)
	require.NoError(t, err)

	// List subtitles
	tracks, err := svc.ListSubtitles(context.Background(), "test-hash")
	assert.NoError(t, err)
	assert.Len(t, tracks, 2)

	// Verify tracks contain expected languages
	languages := make(map[string]bool)
	for _, track := range tracks {
		languages[track.Language] = true
	}
	assert.True(t, languages["en"], "should have English subtitle")
	assert.True(t, languages["es"], "should have Spanish subtitle")
}

func TestVideoServiceDeleteSubtitle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	vttContent := []byte(`WEBVTT

00:00:00.000 --> 00:00:05.000
Test subtitle.
`)

	// Add subtitle
	err = svc.AddSubtitle(context.Background(), "test-hash", core.Subtitle{
		Language: "en",
		Label:    "English",
	}, vttContent)
	require.NoError(t, err)

	// Verify it exists
	_, err = svc.GetSubtitle(context.Background(), "test-hash", "en")
	require.NoError(t, err)

	// Delete subtitle
	err = svc.DeleteSubtitle(context.Background(), "test-hash", "en")
	assert.NoError(t, err)

	// Verify it's gone
	_, err = svc.GetSubtitle(context.Background(), "test-hash", "en")
	assert.ErrorIs(t, err, core.ErrSubtitleNotFound)
}

func TestIsValidWebVTT(t *testing.T) {
	tests := []struct {
		name    string
		content string
		valid   bool
	}{
		{
			name:    "valid simple",
			content: "WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello",
			valid:   true,
		},
		{
			name:    "valid with BOM",
			content: "\ufeffWEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello",
			valid:   true,
		},
		{
			name:    "valid with header text",
			content: "WEBVTT - This is a header\n\n00:00:00.000 --> 00:00:05.000\nHello",
			valid:   true,
		},
		{
			name:    "invalid no signature",
			content: "00:00:00.000 --> 00:00:05.000\nHello",
			valid:   false,
		},
		{
			name:    "invalid wrong signature",
			content: "WEBVTT2\n\n00:00:00.000 --> 00:00:05.000\nHello",
			valid:   false,
		},
		{
			name:    "invalid empty",
			content: "",
			valid:   false,
		},
		{
			name:    "invalid plain text",
			content: "This is just plain text, not subtitles.",
			valid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidWebVTT([]byte(tt.content))
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestSubtitleTypes(t *testing.T) {
	// Test Subtitle type
	subtitle := core.Subtitle{
		Language: "en",
		Label:    "English",
		Default:  true,
		Forced:   false,
	}
	assert.Equal(t, "en", subtitle.Language)
	assert.Equal(t, "English", subtitle.Label)
	assert.True(t, subtitle.Default)
	assert.False(t, subtitle.Forced)

	// Test SubtitleTrack type
	track := core.SubtitleTrack{
		Subtitle:  subtitle,
		BlobHash:  "abc123",
		CreatedAt: 1234567890,
	}
	assert.Equal(t, "en", track.Language)
	assert.Equal(t, "abc123", track.BlobHash)
	assert.Equal(t, int64(1234567890), track.CreatedAt)
}

func TestVideoServiceSubtitleWithBOM(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "video-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	localStorage, err := storage.NewLocalStorage(tempDir)
	require.NoError(t, err)

	memCache := cache.NewMemoryCache(10 * 1024 * 1024)
	log, _ := zap.NewDevelopment()

	svc, err := NewVideoService(localStorage, memCache, VideoConfig{
		WorkDir:    tempDir,
		CDNBaseUrl: "http://localhost:8000",
	}, log)
	require.NoError(t, err)

	// WebVTT with BOM (common in Windows-generated files)
	vttWithBOM := []byte("\ufeffWEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello!")

	err = svc.AddSubtitle(context.Background(), "test-hash", core.Subtitle{
		Language: "en",
		Label:    "English",
	}, vttWithBOM)
	assert.NoError(t, err, "WebVTT with BOM should be accepted")
}
