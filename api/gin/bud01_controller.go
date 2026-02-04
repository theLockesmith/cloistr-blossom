package gin

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
	bud01 "git.coldforge.xyz/coldforge/coldforge-blossom/src/bud-01"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/core"
	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/media"
)

// variantCacheKey generates a cache key for a processed image variant.
func variantCacheKey(hash string, opts media.ProcessOptions) string {
	return fmt.Sprintf("variant:%s_%dx%d_%s_%v", hash, opts.Width, opts.Height, opts.Format, opts.Crop)
}

func getBlob(
	services core.Services,
) gin.HandlerFunc {
	processor := media.NewImageProcessor()

	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		hash := pathParts[0]

		// Check for image processing parameters
		width, _ := strconv.Atoi(ctx.Query("width"))
		height, _ := strconv.Atoi(ctx.Query("height"))
		format := ctx.Query("format")
		thumbnail := ctx.Query("thumbnail")

		// Build processing options
		opts := media.ProcessOptions{
			Width:  width,
			Height: height,
		}

		// Handle thumbnail presets
		switch thumbnail {
		case "small":
			opts.Width, opts.Height, opts.Crop = 150, 150, true
		case "medium":
			opts.Width, opts.Height, opts.Crop = 300, 300, true
		case "large":
			opts.Width, opts.Height, opts.Crop = 600, 600, true
		}

		// Handle format conversion
		switch format {
		case "jpeg", "jpg":
			opts.Format = media.FormatJPEG
		case "png":
			opts.Format = media.FormatPNG
		case "webp":
			opts.Format = media.FormatWebP
		case "gif":
			opts.Format = media.FormatGIF
		}

		needsProcessing := opts.Width > 0 || opts.Height > 0 || opts.Format != ""

		// Check cache for processed variants
		if needsProcessing {
			cacheKey := variantCacheKey(hash, opts)
			if cached, ok := services.Cache().Get(ctx.Request.Context(), cacheKey); ok {
				contentType := media.FormatToContentType(opts.Format)
				if contentType == "" {
					contentType = "image/jpeg"
				}
				ctx.Header("Content-Type", contentType)
				ctx.Header("X-Cache", "HIT")
				_, _ = ctx.Writer.Write(cached)
				ctx.Status(http.StatusOK)
				return
			}
		}

		fileBytes, err := bud01.GetBlob(
			ctx.Request.Context(),
			services,
			hash,
		)
		if err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: err.Error(),
				},
			)
			return
		}

		// If no processing requested, return original
		if !needsProcessing {
			mType := mimetype.Detect(fileBytes)
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		// Check if this is an image
		mType := mimetype.Detect(fileBytes)
		if !media.IsImage(mType.String()) {
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		// Process image
		result, err := processor.Process(bytes.NewReader(fileBytes), opts)
		if err != nil {
			// On processing error, return original
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		// Cache the processed result (1 hour TTL)
		cacheKey := variantCacheKey(hash, opts)
		services.Cache().Set(ctx.Request.Context(), cacheKey, result.Data, time.Hour)

		ctx.Header("Content-Type", result.ContentType)
		ctx.Header("X-Cache", "MISS")
		ctx.Header("X-Image-Width", strconv.Itoa(result.Width))
		ctx.Header("X-Image-Height", strconv.Itoa(result.Height))
		_, _ = ctx.Writer.Write(result.Data)
		ctx.Status(http.StatusOK)
	}
}

func hasBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		_, err := bud01.HasBlob(
			ctx.Request.Context(),
			services,
			pathParts[0],
		)
		if err != nil {
			ctx.AbortWithStatus(http.StatusNotFound)
			return
		}

		ctx.Status(http.StatusOK)
	}
}
