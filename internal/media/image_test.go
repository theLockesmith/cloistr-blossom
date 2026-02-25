package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a test image of given dimensions
func createTestImage(width, height int, format string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Fill with a simple pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.RGBA{
				R: uint8((x * 255) / width),
				G: uint8((y * 255) / height),
				B: 128,
				A: 255,
			}
			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	switch format {
	case "jpeg":
		jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	case "png":
		png.Encode(&buf, img)
	default:
		jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	return buf.Bytes()
}

func TestIsImage(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{
			name:     "jpeg",
			mimeType: "image/jpeg",
			want:     true,
		},
		{
			name:     "png",
			mimeType: "image/png",
			want:     true,
		},
		{
			name:     "gif",
			mimeType: "image/gif",
			want:     true,
		},
		{
			name:     "webp",
			mimeType: "image/webp",
			want:     true,
		},
		{
			name:     "not_an_image_pdf",
			mimeType: "application/pdf",
			want:     false,
		},
		{
			name:     "not_an_image_text",
			mimeType: "text/plain",
			want:     false,
		},
		{
			name:     "not_an_image_video",
			mimeType: "video/mp4",
			want:     false,
		},
		{
			name:     "empty_string",
			mimeType: "",
			want:     false,
		},
		{
			name:     "invalid_mime",
			mimeType: "invalid/type",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsImage(tt.mimeType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatToContentType(t *testing.T) {
	tests := []struct {
		name   string
		format ImageFormat
		want   string
	}{
		{
			name:   "jpeg",
			format: FormatJPEG,
			want:   "image/jpeg",
		},
		{
			name:   "png",
			format: FormatPNG,
			want:   "image/png",
		},
		{
			name:   "gif",
			format: FormatGIF,
			want:   "image/gif",
		},
		{
			name:   "webp",
			format: FormatWebP,
			want:   "image/webp",
		},
		{
			name:   "unknown_format",
			format: ImageFormat("unknown"),
			want:   "image/jpeg", // default
		},
		{
			name:   "empty_format",
			format: ImageFormat(""),
			want:   "image/jpeg", // default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToContentType(tt.format)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMimeTypeToFormat(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     ImageFormat
	}{
		{
			name:     "jpeg",
			mimeType: "image/jpeg",
			want:     FormatJPEG,
		},
		{
			name:     "png",
			mimeType: "image/png",
			want:     FormatPNG,
		},
		{
			name:     "gif",
			mimeType: "image/gif",
			want:     FormatGIF,
		},
		{
			name:     "webp",
			mimeType: "image/webp",
			want:     FormatWebP,
		},
		{
			name:     "unknown_type",
			mimeType: "image/tiff",
			want:     FormatJPEG, // default
		},
		{
			name:     "non_image_type",
			mimeType: "video/mp4",
			want:     FormatJPEG, // default
		},
		{
			name:     "empty_string",
			mimeType: "",
			want:     FormatJPEG, // default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MimeTypeToFormat(tt.mimeType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewImageProcessor(t *testing.T) {
	processor := NewImageProcessor()

	assert.NotNil(t, processor)
	assert.Equal(t, 85, processor.JPEGQuality)
	assert.Equal(t, 80, processor.WebPQuality)
}

func TestImageProcessor_Process_NoResize(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.Data)
	assert.Equal(t, FormatJPEG, result.Format)
	assert.Equal(t, "image/jpeg", result.ContentType)
	assert.Equal(t, 100, result.Width)
	assert.Equal(t, 100, result.Height)
}

func TestImageProcessor_Process_ResizeWidth(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(200, 100, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Width: 100,
	})

	require.NoError(t, err)
	assert.Equal(t, 100, result.Width)
	assert.Equal(t, 50, result.Height) // Aspect ratio preserved
}

func TestImageProcessor_Process_ResizeHeight(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 200, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Height: 100,
	})

	require.NoError(t, err)
	assert.Equal(t, 50, result.Width)  // Aspect ratio preserved
	assert.Equal(t, 100, result.Height)
}

func TestImageProcessor_Process_ResizeBothDimensions(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(400, 300, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Width:  200,
		Height: 150,
	})

	require.NoError(t, err)
	// With both dimensions and no crop, it fits within bounds
	assert.LessOrEqual(t, result.Width, 200)
	assert.LessOrEqual(t, result.Height, 150)
}

func TestImageProcessor_Process_Crop(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(400, 300, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Width:  200,
		Height: 200,
		Crop:   true,
	})

	require.NoError(t, err)
	assert.Equal(t, 200, result.Width)
	assert.Equal(t, 200, result.Height)
}

func TestImageProcessor_Process_FormatConversion_JPEG(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "png")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Format: FormatJPEG,
	})

	require.NoError(t, err)
	assert.Equal(t, FormatJPEG, result.Format)
	assert.Equal(t, "image/jpeg", result.ContentType)
}

func TestImageProcessor_Process_FormatConversion_PNG(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "jpeg")

	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Format: FormatPNG,
	})

	require.NoError(t, err)
	assert.Equal(t, FormatPNG, result.Format)
	assert.Equal(t, "image/png", result.ContentType)
}

func TestImageProcessor_Process_CustomQuality(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "jpeg")

	// High quality should produce larger file
	resultHighQuality, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Format:  FormatJPEG,
		Quality: 95,
	})
	require.NoError(t, err)

	// Low quality should produce smaller file
	resultLowQuality, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Format:  FormatJPEG,
		Quality: 10,
	})
	require.NoError(t, err)

	assert.Greater(t, len(resultHighQuality.Data), len(resultLowQuality.Data))
}

func TestImageProcessor_Process_InvalidImage(t *testing.T) {
	processor := NewImageProcessor()
	invalidData := []byte("this is not an image")

	_, err := processor.Process(bytes.NewReader(invalidData), ProcessOptions{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode image")
}

func TestImageProcessor_Process_EmptyInput(t *testing.T) {
	processor := NewImageProcessor()

	_, err := processor.Process(bytes.NewReader([]byte{}), ProcessOptions{})

	assert.Error(t, err)
}

func TestImageProcessor_Process_WebPFallback(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "jpeg")

	// Request WebP format (falls back to JPEG in current implementation)
	result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
		Format: FormatWebP,
	})

	require.NoError(t, err)
	// Should fall back to JPEG
	assert.Equal(t, FormatJPEG, result.Format)
	assert.Equal(t, "image/jpeg", result.ContentType)
}

func TestImageProcessor_GenerateThumbnail(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(800, 600, "jpeg")

	tests := []struct {
		name string
		size ThumbnailSize
	}{
		{
			name: "small",
			size: ThumbnailSmall,
		},
		{
			name: "medium",
			size: ThumbnailMedium,
		},
		{
			name: "large",
			size: ThumbnailLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processor.GenerateThumbnail(bytes.NewReader(testData), tt.size)

			require.NoError(t, err)
			assert.Equal(t, tt.size.Width, result.Width)
			assert.Equal(t, tt.size.Height, result.Height)
			assert.Equal(t, FormatJPEG, result.Format)
		})
	}
}

func TestImageProcessor_GenerateThumbnail_InvalidImage(t *testing.T) {
	processor := NewImageProcessor()
	invalidData := []byte("not an image")

	_, err := processor.GenerateThumbnail(bytes.NewReader(invalidData), ThumbnailSmall)

	assert.Error(t, err)
}

func TestImageProcessor_Resize(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(800, 600, "jpeg")

	result, err := processor.Resize(bytes.NewReader(testData), 400, 300)

	require.NoError(t, err)
	assert.LessOrEqual(t, result.Width, 400)
	assert.LessOrEqual(t, result.Height, 300)
	// Aspect ratio should be preserved
	assert.InDelta(t, 800.0/600.0, float64(result.Width)/float64(result.Height), 0.1)
}

func TestImageProcessor_Resize_PreservesAspectRatio(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(1600, 900, "jpeg") // 16:9 aspect ratio

	result, err := processor.Resize(bytes.NewReader(testData), 800, 600)

	require.NoError(t, err)
	// Should fit within 800x600 while preserving 16:9 aspect ratio
	assert.LessOrEqual(t, result.Width, 800)
	assert.LessOrEqual(t, result.Height, 600)
	assert.InDelta(t, 16.0/9.0, float64(result.Width)/float64(result.Height), 0.1)
}

func TestImageProcessor_ConvertFormat(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(100, 100, "jpeg")

	tests := []struct {
		name       string
		format     ImageFormat
		wantFormat ImageFormat // Expected format (may differ if fallback occurs)
	}{
		{
			name:       "to_png",
			format:     FormatPNG,
			wantFormat: FormatPNG,
		},
		{
			name:       "to_gif",
			format:     FormatGIF,
			wantFormat: FormatGIF,
		},
		{
			name:       "to_webp",
			format:     FormatWebP,
			wantFormat: FormatJPEG, // Falls back to JPEG
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processor.ConvertFormat(bytes.NewReader(testData), tt.format)

			require.NoError(t, err)
			assert.Equal(t, tt.wantFormat, result.Format)
		})
	}
}

func TestImageProcessor_GetDimensions(t *testing.T) {
	processor := NewImageProcessor()
	testData := createTestImage(640, 480, "jpeg")

	width, height, err := processor.GetDimensions(bytes.NewReader(testData))

	require.NoError(t, err)
	assert.Equal(t, 640, width)
	assert.Equal(t, 480, height)
}

func TestImageProcessor_GetDimensions_InvalidImage(t *testing.T) {
	processor := NewImageProcessor()
	invalidData := []byte("not an image")

	_, _, err := processor.GetDimensions(bytes.NewReader(invalidData))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode image config")
}

func TestImageProcessor_GetDimensions_EmptyInput(t *testing.T) {
	processor := NewImageProcessor()

	_, _, err := processor.GetDimensions(bytes.NewReader([]byte{}))

	assert.Error(t, err)
}

func TestThumbnailSizes(t *testing.T) {
	// Verify standard thumbnail sizes are defined correctly
	assert.Equal(t, "small", ThumbnailSmall.Name)
	assert.Equal(t, 150, ThumbnailSmall.Width)
	assert.Equal(t, 150, ThumbnailSmall.Height)

	assert.Equal(t, "medium", ThumbnailMedium.Name)
	assert.Equal(t, 300, ThumbnailMedium.Width)
	assert.Equal(t, 300, ThumbnailMedium.Height)

	assert.Equal(t, "large", ThumbnailLarge.Name)
	assert.Equal(t, 600, ThumbnailLarge.Width)
	assert.Equal(t, 600, ThumbnailLarge.Height)

	// Verify DefaultThumbnailSizes contains all sizes
	assert.Len(t, DefaultThumbnailSizes, 3)
	assert.Contains(t, DefaultThumbnailSizes, ThumbnailSmall)
	assert.Contains(t, DefaultThumbnailSizes, ThumbnailMedium)
	assert.Contains(t, DefaultThumbnailSizes, ThumbnailLarge)
}

func TestImageProcessor_EdgeCases(t *testing.T) {
	processor := NewImageProcessor()

	t.Run("very_small_image", func(t *testing.T) {
		testData := createTestImage(1, 1, "jpeg")
		result, err := processor.Resize(bytes.NewReader(testData), 10, 10)

		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("tall_image", func(t *testing.T) {
		testData := createTestImage(100, 1000, "jpeg")
		result, err := processor.Resize(bytes.NewReader(testData), 50, 500)

		require.NoError(t, err)
		assert.LessOrEqual(t, result.Width, 50)
		assert.LessOrEqual(t, result.Height, 500)
	})

	t.Run("wide_image", func(t *testing.T) {
		testData := createTestImage(1000, 100, "jpeg")
		result, err := processor.Resize(bytes.NewReader(testData), 500, 50)

		require.NoError(t, err)
		assert.LessOrEqual(t, result.Width, 500)
		assert.LessOrEqual(t, result.Height, 50)
	})

	t.Run("resize_larger_than_original", func(t *testing.T) {
		testData := createTestImage(100, 100, "jpeg")
		result, err := processor.Resize(bytes.NewReader(testData), 200, 200)

		require.NoError(t, err)
		// Fit should not enlarge beyond original dimensions
		assert.LessOrEqual(t, result.Width, 200)
		assert.LessOrEqual(t, result.Height, 200)
	})
}

func TestImageProcessor_QualityDefaults(t *testing.T) {
	processor := NewImageProcessor()

	t.Run("default_jpeg_quality", func(t *testing.T) {
		testData := createTestImage(100, 100, "jpeg")
		result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
			Format: FormatJPEG,
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("custom_quality_override", func(t *testing.T) {
		testData := createTestImage(100, 100, "jpeg")
		result, err := processor.Process(bytes.NewReader(testData), ProcessOptions{
			Format:  FormatJPEG,
			Quality: 50,
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}
