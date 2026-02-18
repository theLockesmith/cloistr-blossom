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
