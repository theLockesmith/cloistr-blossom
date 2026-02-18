package media

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/disintegration/imaging"
	"golang.org/x/image/webp"
)

// ImageFormat represents an output image format.
type ImageFormat string

const (
	FormatJPEG ImageFormat = "jpeg"
	FormatPNG  ImageFormat = "png"
	FormatWebP ImageFormat = "webp"
	FormatGIF  ImageFormat = "gif"
)

// ThumbnailSize represents a standard thumbnail size.
type ThumbnailSize struct {
	Name   string
	Width  int
	Height int
}

// Standard thumbnail sizes.
var (
	ThumbnailSmall  = ThumbnailSize{Name: "small", Width: 150, Height: 150}
	ThumbnailMedium = ThumbnailSize{Name: "medium", Width: 300, Height: 300}
	ThumbnailLarge  = ThumbnailSize{Name: "large", Width: 600, Height: 600}

	DefaultThumbnailSizes = []ThumbnailSize{
		ThumbnailSmall,
		ThumbnailMedium,
		ThumbnailLarge,
	}
)

// ImageProcessor handles image transformations.
type ImageProcessor struct {
	// Quality for JPEG encoding (1-100)
	JPEGQuality int
	// Quality for WebP encoding (1-100)
	WebPQuality int
}

// NewImageProcessor creates a new image processor with default settings.
func NewImageProcessor() *ImageProcessor {
	return &ImageProcessor{
		JPEGQuality: 85,
		WebPQuality: 80,
	}
}

// ProcessOptions defines options for image processing.
type ProcessOptions struct {
	Width   int         // Target width (0 = preserve aspect ratio)
	Height  int         // Target height (0 = preserve aspect ratio)
	Format  ImageFormat // Output format
	Quality int         // Output quality (1-100, 0 = default)
	Crop    bool        // Center crop to exact dimensions
}

// ProcessResult contains the result of image processing.
type ProcessResult struct {
	Data        []byte
	Width       int
	Height      int
	Format      ImageFormat
	ContentType string
}

// IsImage checks if the MIME type is a supported image format.
func IsImage(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// Process transforms an image according to the given options.
func (p *ImageProcessor) Process(data io.Reader, opts ProcessOptions) (*ProcessResult, error) {
	// Decode the source image
	img, format, err := image.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	// Determine output format
	outputFormat := opts.Format
	if outputFormat == "" {
		outputFormat = ImageFormat(format)
	}

	// Resize if dimensions specified
	if opts.Width > 0 || opts.Height > 0 {
		bounds := img.Bounds()
		origWidth := bounds.Dx()
		origHeight := bounds.Dy()

		targetWidth := opts.Width
		targetHeight := opts.Height

		// If only one dimension specified, calculate the other to preserve aspect ratio
		if targetWidth > 0 && targetHeight == 0 {
			targetHeight = origHeight * targetWidth / origWidth
			if targetHeight == 0 {
				targetHeight = 1
			}
		} else if targetHeight > 0 && targetWidth == 0 {
			targetWidth = origWidth * targetHeight / origHeight
			if targetWidth == 0 {
				targetWidth = 1
			}
		}

		if opts.Crop {
			img = imaging.Fill(img, targetWidth, targetHeight, imaging.Center, imaging.Lanczos)
		} else {
			img = imaging.Fit(img, targetWidth, targetHeight, imaging.Lanczos)
		}
	}

	// Encode to output format
	var buf bytes.Buffer
	quality := opts.Quality
	if quality == 0 {
		quality = p.JPEGQuality
	}

	switch outputFormat {
	case FormatJPEG:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	case FormatPNG:
		encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
		err = encoder.Encode(&buf, img)
	case FormatGIF:
		err = gif.Encode(&buf, img, nil)
	case FormatWebP:
		// WebP encoding not directly supported by standard library
		// Fall back to JPEG for now
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		outputFormat = FormatJPEG
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		outputFormat = FormatJPEG
	}

	if err != nil {
		return nil, fmt.Errorf("encode image: %w", err)
	}

	bounds := img.Bounds()
	return &ProcessResult{
		Data:        buf.Bytes(),
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Format:      outputFormat,
		ContentType: FormatToContentType(outputFormat),
	}, nil
}

// GenerateThumbnail creates a square thumbnail of the specified size.
func (p *ImageProcessor) GenerateThumbnail(data io.Reader, size ThumbnailSize) (*ProcessResult, error) {
	return p.Process(data, ProcessOptions{
		Width:  size.Width,
		Height: size.Height,
		Crop:   true,
		Format: FormatJPEG,
	})
}

// Resize resizes an image to fit within the given dimensions while preserving aspect ratio.
func (p *ImageProcessor) Resize(data io.Reader, width, height int) (*ProcessResult, error) {
	return p.Process(data, ProcessOptions{
		Width:  width,
		Height: height,
		Crop:   false,
	})
}

// ConvertFormat converts an image to a different format.
func (p *ImageProcessor) ConvertFormat(data io.Reader, format ImageFormat) (*ProcessResult, error) {
	return p.Process(data, ProcessOptions{
		Format: format,
	})
}

// GetDimensions returns the dimensions of an image without processing it.
func (p *ImageProcessor) GetDimensions(data io.Reader) (width, height int, err error) {
	config, _, err := image.DecodeConfig(data)
	if err != nil {
		return 0, 0, fmt.Errorf("decode image config: %w", err)
	}
	return config.Width, config.Height, nil
}

// DecodeWebP decodes a WebP image.
func DecodeWebP(data io.Reader) (image.Image, error) {
	return webp.Decode(data)
}

// FormatToContentType converts an ImageFormat to its MIME content type.
func FormatToContentType(format ImageFormat) string {
	switch format {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	case FormatGIF:
		return "image/gif"
	case FormatWebP:
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// MimeTypeToFormat converts a MIME type to an ImageFormat.
func MimeTypeToFormat(mimeType string) ImageFormat {
	switch mimeType {
	case "image/jpeg":
		return FormatJPEG
	case "image/png":
		return FormatPNG
	case "image/gif":
		return FormatGIF
	case "image/webp":
		return FormatWebP
	default:
		return FormatJPEG
	}
}
