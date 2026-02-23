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
	bud01 "git.coldforge.xyz/coldforge/cloistr-blossom/src/bud-01"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/media"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// variantCacheKey generates a cache key for a processed image variant.
func variantCacheKey(hash string, opts media.ProcessOptions) string {
	return fmt.Sprintf("variant:%s_%dx%d_%s_%v", hash, opts.Width, opts.Height, opts.Format, opts.Crop)
}

// setSecurityHeaders sets security headers for blob responses.
// Safe media types (images, video, audio) are served inline for preview.
// All other types are forced to download to prevent XSS/execution.
func setSecurityHeaders(ctx *gin.Context, contentType, hash string) {
	// Always prevent MIME sniffing
	ctx.Header("X-Content-Type-Options", "nosniff")

	// Sandbox content to prevent script execution even if rendered
	ctx.Header("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; media-src 'self'")

	// Determine if content is safe to display inline
	safeForInline := isSafeForInline(contentType)

	if safeForInline {
		ctx.Header("Content-Disposition", "inline")
	} else {
		// Force download for potentially dangerous content
		ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", hash))
	}
}

// isSafeForInline returns true for media types that are safe to render in browser.
func isSafeForInline(contentType string) bool {
	// Images (excluding SVG which can contain scripts)
	if strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml" {
		return true
	}
	// Video
	if strings.HasPrefix(contentType, "video/") {
		return true
	}
	// Audio
	if strings.HasPrefix(contentType, "audio/") {
		return true
	}
	// PDF (rendered by browser PDF viewer, sandboxed)
	if contentType == "application/pdf" {
		return true
	}
	return false
}

func getBlob(
	services core.Services,
) gin.HandlerFunc {
	processor := media.NewImageProcessor()

	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("hash"), ".")
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

		// CDN redirect for unprocessed blobs
		// Only redirect if CDN is enabled, redirect is configured, and no processing is needed
		if !needsProcessing && services.CDN().ShouldRedirect() {
			cdnURL, err := services.CDN().GetBlobURL(ctx.Request.Context(), hash, "")
			if err == nil && cdnURL != "" {
				ctx.Redirect(http.StatusFound, cdnURL)
				metrics.DownloadsTotal.WithLabelValues("cdn_redirect").Inc()
				return
			}
		}

		// Check cache for processed variants
		if needsProcessing {
			cacheKey := variantCacheKey(hash, opts)
			if cached, ok := services.Cache().Get(ctx.Request.Context(), cacheKey); ok {
				contentType := media.FormatToContentType(opts.Format)
				if contentType == "" {
					contentType = "image/jpeg"
				}
				setSecurityHeaders(ctx, contentType, hash)
				ctx.Header("Content-Type", contentType)
				ctx.Header("X-Cache", "HIT")
				_, _ = ctx.Writer.Write(cached)
				ctx.Status(http.StatusOK)
				metrics.DownloadsTotal.WithLabelValues("success").Inc()
				metrics.DownloadBytes.Add(float64(len(cached)))
				return
			}
		}

		fileBytes, err := bud01.GetBlob(
			ctx.Request.Context(),
			services,
			hash,
		)
		if err != nil {
			metrics.DownloadsTotal.WithLabelValues("error").Inc()
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
			setSecurityHeaders(ctx, mType.String(), hash)
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			metrics.DownloadsTotal.WithLabelValues("success").Inc()
			metrics.DownloadBytes.Add(float64(len(fileBytes)))
			return
		}

		// Check if this is an image
		mType := mimetype.Detect(fileBytes)
		if !media.IsImage(mType.String()) {
			setSecurityHeaders(ctx, mType.String(), hash)
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			metrics.DownloadsTotal.WithLabelValues("success").Inc()
			metrics.DownloadBytes.Add(float64(len(fileBytes)))
			return
		}

		// Process image
		result, err := processor.Process(bytes.NewReader(fileBytes), opts)
		if err != nil {
			// On processing error, return original
			setSecurityHeaders(ctx, mType.String(), hash)
			ctx.Header("Content-Type", mType.String())
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			metrics.DownloadsTotal.WithLabelValues("success").Inc()
			metrics.DownloadBytes.Add(float64(len(fileBytes)))
			return
		}

		// Cache the processed result (1 hour TTL)
		cacheKey := variantCacheKey(hash, opts)
		services.Cache().Set(ctx.Request.Context(), cacheKey, result.Data, time.Hour)

		setSecurityHeaders(ctx, result.ContentType, hash)
		ctx.Header("Content-Type", result.ContentType)
		ctx.Header("X-Cache", "MISS")
		ctx.Header("X-Image-Width", strconv.Itoa(result.Width))
		ctx.Header("X-Image-Height", strconv.Itoa(result.Height))
		_, _ = ctx.Writer.Write(result.Data)
		ctx.Status(http.StatusOK)
		metrics.DownloadsTotal.WithLabelValues("success").Inc()
		metrics.DownloadBytes.Add(float64(len(result.Data)))
	}
}

func hasBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("hash"), ".")
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
