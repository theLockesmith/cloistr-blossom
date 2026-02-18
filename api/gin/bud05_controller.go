package gin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

// uploadMedia handles PUT /media for BUD-05 media optimization.
// It accepts binary media data, processes/optimizes it, and returns a blob descriptor.
func uploadMedia(
	services core.Services,
	cdnBaseUrl string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Check if pubkey is blocked
		pubkey := ctx.GetString("pk")
		if pubkey != "" {
			isBlocked, err := services.Moderation().IsBlocked(ctx.Request.Context(), pubkey)
			if err == nil && isBlocked {
				metrics.BlockedUploadsTotal.Inc()
				ctx.AbortWithStatusJSON(
					http.StatusForbidden,
					apiError{Message: "your account has been blocked due to terms of service violation"},
				)
				return
			}
		}

		// Read request body
		bodyBytes, err := io.ReadAll(ctx.Request.Body)
		defer ctx.Request.Body.Close()
		if err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{Message: fmt.Sprintf("failed to read request body: %s", err.Error())},
			)
			return
		}

		if len(bodyBytes) == 0 {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{Message: "request body is empty"},
			)
			return
		}

		// Detect MIME type from content
		mtype := mimetype.Detect(bodyBytes)
		contentType := mtype.String()

		// Allow Content-Type header to override detection for well-known image types
		if ct := ctx.GetHeader("Content-Type"); ct != "" {
			switch ct {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
				contentType = ct
			}
		}

		// Check if media type is supported
		if !services.Media().IsSupported(contentType) {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{Message: fmt.Sprintf("unsupported media type: %s", contentType)},
			)
			return
		}

		// Parse processing options from query parameters
		opts := parseMediaOptions(ctx)

		// Process the media
		result, err := services.Media().ProcessImage(
			ctx.Request.Context(),
			bytes.NewReader(bodyBytes),
			contentType,
			opts,
		)
		if err != nil {
			metrics.ErrorsTotal.WithLabelValues("media_processing").Inc()
			ctx.AbortWithStatusJSON(
				http.StatusInternalServerError,
				apiError{Message: fmt.Sprintf("failed to process media: %s", err.Error())},
			)
			return
		}

		// Check quota for the user
		if pubkey != "" {
			if err := services.Quota().CheckQuota(ctx.Request.Context(), pubkey, int64(len(result.Data))); err != nil {
				if errors.Is(err, core.ErrQuotaExceeded) {
					ctx.AbortWithStatusJSON(
						http.StatusPaymentRequired,
						apiError{Message: "storage quota exceeded"},
					)
					return
				}
				if errors.Is(err, core.ErrUserBanned) {
					ctx.AbortWithStatusJSON(
						http.StatusForbidden,
						apiError{Message: "user is banned"},
					)
					return
				}
				ctx.AbortWithStatusJSON(
					http.StatusInternalServerError,
					apiError{Message: "failed to check quota"},
				)
				return
			}
		}

		// Calculate hash of original for verification
		originalHash := sha256.Sum256(bodyBytes)
		originalHashStr := hex.EncodeToString(originalHash[:])

		// Check if x tag matches (if provided in auth)
		if xTag := ctx.GetString("x"); xTag != "" && xTag != originalHashStr {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{Message: "x tag does not match uploaded content hash"},
			)
			return
		}

		// Create URL for the processed blob
		url := cdnBaseUrl + "/" + result.Hash

		// Save the processed blob
		created := time.Now().Unix()
		blob, err := services.Blob().Save(
			ctx.Request.Context(),
			pubkey,
			result.Hash,
			url,
			int64(len(result.Data)),
			result.ContentType,
			result.Data,
			created,
			core.EncryptionModeNone, // Media endpoint stores processed files unencrypted
		)
		if err != nil {
			metrics.ErrorsTotal.WithLabelValues("media_save").Inc()
			ctx.AbortWithStatusJSON(
				http.StatusInternalServerError,
				apiError{Message: fmt.Sprintf("failed to save processed media: %s", err.Error())},
			)
			return
		}

		// Update quota usage
		if pubkey != "" {
			_ = services.Quota().IncrementUsage(ctx.Request.Context(), pubkey, int64(len(result.Data)))
		}

		// Record metrics
		metrics.UploadsTotal.WithLabelValues("success", "media").Inc()
		metrics.UploadBytes.Add(float64(len(result.Data)))

		// Return blob descriptor with additional processing info
		response := fromDomainBlobDescriptor(blob)
		response.NIP94FileMetadata = &nip94FileMetadata{
			Url:            url,
			MimeType:       result.ContentType,
			Sha256:         result.Hash,
			OriginalSha256: originalHashStr,
			Dimension:      ptr(fmt.Sprintf("%dx%d", result.Width, result.Height)),
		}

		ctx.JSON(http.StatusOK, response)
	}
}

// mediaRequirements handles HEAD /media for BUD-05.
// Similar to upload requirements but for media processing.
func mediaRequirements(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		maxSize := services.Settings().GetMaxUploadSizeBytes()

		ctx.Header("X-Max-Upload-Size", fmt.Sprintf("%d", maxSize))
		ctx.Header("X-Supported-Types", "image/jpeg,image/png,image/gif,image/webp")
		ctx.Status(http.StatusOK)
	}
}

// getThumbnail handles GET /:hash/thumb for thumbnail generation.
func getThumbnail(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := ctx.Param("hash")
		if hash == "" {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{Message: "hash is required"},
			)
			return
		}

		// Parse size from query params
		width := 150
		height := 150
		if w := ctx.Query("w"); w != "" {
			if parsed, err := strconv.Atoi(w); err == nil && parsed > 0 && parsed <= 1200 {
				width = parsed
			}
		}
		if h := ctx.Query("h"); h != "" {
			if parsed, err := strconv.Atoi(h); err == nil && parsed > 0 && parsed <= 1200 {
				height = parsed
			}
		}

		// Generate thumbnail
		result, err := services.Media().GetThumbnail(ctx.Request.Context(), hash, width, height)
		if err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusNotFound,
				apiError{Message: "blob not found or not an image"},
			)
			return
		}

		ctx.Header("Content-Type", result.ContentType)
		ctx.Header("Content-Length", fmt.Sprintf("%d", len(result.Data)))
		ctx.Header("Cache-Control", "public, max-age=31536000") // Cache for 1 year
		ctx.Data(http.StatusOK, result.ContentType, result.Data)
	}
}

// parseMediaOptions extracts processing options from query parameters.
func parseMediaOptions(ctx *gin.Context) *core.MediaProcessOptions {
	opts := &core.MediaProcessOptions{}

	if w := ctx.Query("w"); w != "" {
		if width, err := strconv.Atoi(w); err == nil && width > 0 && width <= 4096 {
			opts.Width = width
		}
	}

	if h := ctx.Query("h"); h != "" {
		if height, err := strconv.Atoi(h); err == nil && height > 0 && height <= 4096 {
			opts.Height = height
		}
	}

	if q := ctx.Query("q"); q != "" {
		if quality, err := strconv.Atoi(q); err == nil && quality >= 1 && quality <= 100 {
			opts.Quality = quality
		}
	}

	if f := ctx.Query("f"); f != "" {
		switch f {
		case "jpeg", "jpg":
			opts.Format = "jpeg"
		case "png":
			opts.Format = "png"
		case "webp":
			opts.Format = "webp"
		}
	}

	return opts
}

// ptr is a helper to create a pointer to a string.
func ptr(s string) *string {
	return &s
}
