package gin

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/media"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	bud01 "git.aegis-hq.xyz/coldforge/cloistr-blossom/src/bud-01"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
)

// variantCacheKey generates a cache key for a processed image variant.
func variantCacheKey(hash string, opts media.ProcessOptions) string {
	return fmt.Sprintf("variant:%s_%dx%d_%s_%v", hash, opts.Width, opts.Height, opts.Format, opts.Crop)
}

// blobContentType returns the Content-Type to serve for a blob: the stored
// type when it parses as a media type, otherwise a sniff of the content,
// otherwise application/octet-stream. content is only called when the stored
// type is unusable, so HEAD can avoid loading the bytes in the common case.
//
// The stored type is not trusted for safety: the inline/attachment decision,
// nosniff and the sandbox CSP in setSecurityHeaders are applied to whatever
// this returns.
func blobContentType(stored string, content func() []byte) string {
	if mt, params, err := mime.ParseMediaType(stored); err == nil && strings.Contains(mt, "/") {
		if formatted := mime.FormatMediaType(mt, params); formatted != "" {
			return formatted
		}
	}
	if data := content(); data != nil {
		return mimetype.Detect(data).String()
	}
	return "application/octet-stream"
}

// setSecurityHeaders sets security headers for blob responses. contentType
// must be the Content-Type actually served. Safe media types (images except
// SVG, video, audio, PDF) are served inline for preview; everything else is
// forced to download to prevent XSS/execution.
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

// isSafeForInline returns true for media types that are safe to render in
// browser. It is an allowlist: unparseable types, any +xml type and any SVG
// variant (SVG can carry scripts) are not safe.
func isSafeForInline(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || strings.HasSuffix(mt, "+xml") || strings.HasPrefix(mt, "image/svg") {
		return false
	}
	if strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") {
		return true
	}
	// PDF (rendered by browser PDF viewer, sandboxed)
	return mt == "application/pdf"
}

// processOptionsFromQuery reads the image processing parameters of a blob
// request and reports whether any processing was requested.
func processOptionsFromQuery(ctx *gin.Context) (media.ProcessOptions, bool) {
	width, _ := strconv.Atoi(ctx.Query("width"))
	height, _ := strconv.Atoi(ctx.Query("height"))

	opts := media.ProcessOptions{
		Width:  width,
		Height: height,
	}

	// Handle thumbnail presets
	switch ctx.Query("thumbnail") {
	case "small":
		opts.Width, opts.Height, opts.Crop = 150, 150, true
	case "medium":
		opts.Width, opts.Height, opts.Crop = 300, 300, true
	case "large":
		opts.Width, opts.Height, opts.Crop = 600, 600, true
	}

	// Handle format conversion
	switch ctx.Query("format") {
	case "jpeg", "jpg":
		opts.Format = media.FormatJPEG
	case "png":
		opts.Format = media.FormatPNG
	case "webp":
		opts.Format = media.FormatWebP
	case "gif":
		opts.Format = media.FormatGIF
	}

	return opts, opts.Width > 0 || opts.Height > 0 || opts.Format != ""
}

// redirectToCDN redirects an unprocessed blob request to the CDN when CDN
// redirects are enabled, and reports whether it did. GET and HEAD share it so
// both answer with the same status and Location.
func redirectToCDN(ctx *gin.Context, services core.Services, hash string) bool {
	if !services.CDN().ShouldRedirect() {
		return false
	}
	cdnURL, err := services.CDN().GetBlobURL(ctx.Request.Context(), hash, "")
	if err != nil || cdnURL == "" {
		return false
	}
	ctx.Redirect(http.StatusFound, cdnURL)
	if ctx.Request.Method == http.MethodGet {
		metrics.DownloadsTotal.WithLabelValues("cdn_redirect").Inc()
	}
	return true
}

func getBlob(
	services core.Services,
) gin.HandlerFunc {
	processor := media.NewImageProcessor()

	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("hash"), ".")
		hash := pathParts[0]
		if !sha256Regex.MatchString(hash) {
			metrics.DownloadsTotal.WithLabelValues("error").Inc()
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash must be 64 hex characters").Abort(ctx)
			return
		}

		opts, needsProcessing := processOptionsFromQuery(ctx)

		if !needsProcessing && redirectToCDN(ctx, services, hash) {
			return
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
				ctx.Header("Content-Length", strconv.Itoa(len(cached)))
				ctx.Header("X-Cache", "HIT")
				_, _ = ctx.Writer.Write(cached)
				ctx.Status(http.StatusOK)
				metrics.DownloadsTotal.WithLabelValues("success").Inc()
				metrics.DownloadBytes.Add(float64(len(cached)))
				return
			}
		}

		blob, err := bud01.GetBlob(
			ctx.Request.Context(),
			services,
			hash,
		)
		if err != nil {
			// Check federation for fallback URLs
			if federation := services.Federation(); federation != nil && federation.IsEnabled() {
				fallbackURLs, fallbackErr := federation.GetFallbackURLs(ctx.Request.Context(), hash)
				if fallbackErr == nil && len(fallbackURLs) > 0 {
					// Return 404 with fallback URLs in header
					ctx.Header("X-Fallback-URLs", strings.Join(fallbackURLs, ", "))
					metrics.DownloadsTotal.WithLabelValues("fallback").Inc()
					clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found locally, check X-Fallback-URLs header for alternative sources").Abort(ctx)
					return
				}
			}

			// bud01.GetBlob reports every lookup failure as not found,
			// matching HEAD.
			metrics.DownloadsTotal.WithLabelValues("error").Inc()
			clerrors.NotFound(clerrors.CodeResourceNotFound, err.Error()).Abort(ctx)
			return
		}

		fileBytes := blob.Blob
		writeOriginal := func() {
			contentType := blobContentType(blob.Type, func() []byte { return fileBytes })
			setSecurityHeaders(ctx, contentType, hash)
			ctx.Header("Content-Type", contentType)
			ctx.Header("Content-Length", strconv.Itoa(len(fileBytes)))
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			metrics.DownloadsTotal.WithLabelValues("success").Inc()
			metrics.DownloadBytes.Add(float64(len(fileBytes)))
		}

		// If no processing requested, return original
		if !needsProcessing {
			writeOriginal()
			return
		}

		// Only image content is processed; the decision is made on the bytes.
		if !media.IsImage(mimetype.Detect(fileBytes).String()) {
			writeOriginal()
			return
		}

		// Process image
		result, err := processor.Process(bytes.NewReader(fileBytes), opts)
		if err != nil {
			// On processing error, return original
			writeOriginal()
			return
		}

		// Cache the processed result (1 hour TTL)
		cacheKey := variantCacheKey(hash, opts)
		services.Cache().Set(ctx.Request.Context(), cacheKey, result.Data, time.Hour)

		setSecurityHeaders(ctx, result.ContentType, hash)
		ctx.Header("Content-Type", result.ContentType)
		ctx.Header("Content-Length", strconv.Itoa(len(result.Data)))
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
		hash := pathParts[0]

		if _, needsProcessing := processOptionsFromQuery(ctx); !needsProcessing && redirectToCDN(ctx, services, hash) {
			return
		}

		blob, err := bud01.GetBlobDescriptor(
			ctx.Request.Context(),
			services,
			hash,
		)
		if err != nil {
			ctx.AbortWithStatus(http.StatusNotFound)
			return
		}

		// Send the same Content-Type and security headers GET would. The
		// content is loaded only when the stored type is unusable and GET
		// would sniff it.
		contentType := blobContentType(blob.Type, func() []byte {
			full, err := bud01.GetBlob(ctx.Request.Context(), services, hash)
			if err != nil {
				return nil
			}
			return full.Blob
		})
		setSecurityHeaders(ctx, contentType, hash)
		ctx.Header("Content-Type", contentType)
		ctx.Header("Content-Length", strconv.FormatInt(blob.Size, 10))
		ctx.Status(http.StatusOK)
	}
}
