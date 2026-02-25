package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDefaultQualities(t *testing.T) {
	tests := []struct {
		name     string
		codec    VideoCodec
		expected []VideoQuality
	}{
		{
			name:     "H.264 codec",
			codec:    CodecH264,
			expected: DefaultQualities,
		},
		{
			name:     "HEVC codec",
			codec:    CodecHEVC,
			expected: HEVCDefaultQualities,
		},
		{
			name:     "AV1 codec",
			codec:    CodecAV1,
			expected: AV1DefaultQualities,
		},
		{
			name:     "unknown codec defaults to H.264",
			codec:    VideoCodec("unknown"),
			expected: DefaultQualities,
		},
		{
			name:     "empty codec defaults to H.264",
			codec:    VideoCodec(""),
			expected: DefaultQualities,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetDefaultQualities(tt.codec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVideoQualityPresets(t *testing.T) {
	// Test H.264 presets
	t.Run("H.264 presets", func(t *testing.T) {
		assert.Equal(t, "720p", Quality720p.Name)
		assert.Equal(t, 1280, Quality720p.Width)
		assert.Equal(t, 720, Quality720p.Height)
		assert.Equal(t, 2500, Quality720p.VideoBitrate)

		assert.Len(t, DefaultQualities, 3)
	})

	// Test HEVC presets have lower bitrates
	t.Run("HEVC presets are more efficient", func(t *testing.T) {
		assert.Less(t, HEVCQuality720p.VideoBitrate, Quality720p.VideoBitrate)
		assert.Less(t, HEVCQuality480p.VideoBitrate, Quality480p.VideoBitrate)

		assert.Len(t, HEVCDefaultQualities, 3)
	})

	// Test AV1 presets have even lower bitrates
	t.Run("AV1 presets are most efficient", func(t *testing.T) {
		assert.Less(t, AV1Quality720p.VideoBitrate, HEVCQuality720p.VideoBitrate)
		assert.Less(t, AV1Quality480p.VideoBitrate, HEVCQuality480p.VideoBitrate)

		assert.Len(t, AV1DefaultQualities, 3)
	})
}

func TestHWAccelTypes(t *testing.T) {
	assert.Equal(t, HWAccelType("none"), HWAccelNone)
	assert.Equal(t, HWAccelType("nvenc"), HWAccelNVENC)
	assert.Equal(t, HWAccelType("qsv"), HWAccelQSV)
	assert.Equal(t, HWAccelType("vaapi"), HWAccelVAAPI)
	assert.Equal(t, HWAccelType("auto"), HWAccelAuto)
}

func TestVideoCodecs(t *testing.T) {
	assert.Equal(t, VideoCodec("h264"), CodecH264)
	assert.Equal(t, VideoCodec("hevc"), CodecHEVC)
	assert.Equal(t, VideoCodec("av1"), CodecAV1)
}

func TestTranscodeStatus(t *testing.T) {
	assert.Equal(t, TranscodeStatus("pending"), TranscodeStatusPending)
	assert.Equal(t, TranscodeStatus("processing"), TranscodeStatusProcessing)
	assert.Equal(t, TranscodeStatus("complete"), TranscodeStatusComplete)
	assert.Equal(t, TranscodeStatus("failed"), TranscodeStatusFailed)
}
