package gin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
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
				clerrors.Forbidden(clerrors.CodeAccessDenied, "your account has been blocked due to terms of service violation").Abort(ctx)
				return
			}
		}

		// Read request body
		bodyBytes, err := io.ReadAll(ctx.Request.Body)
		defer ctx.Request.Body.Close()
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to read request body: %s", err.Error())).Abort(ctx)
			return
		}

		if len(bodyBytes) == 0 {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "request body is empty").Abort(ctx)
			return
		}

		// Validate optional X-Expiration header before processing/storing.
		expiresAt, hasExpiration, ok := parseUploadExpiration(ctx)
		if !ok {
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
			clerrors.BadRequest(clerrors.CodeInvalidFormat, fmt.Sprintf("unsupported media type: %s", contentType)).Abort(ctx)
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
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to process media: %s", err.Error())).Abort(ctx)
			return
		}

		// Check quota for the user
		if pubkey != "" {
			if err := services.Quota().CheckQuota(ctx.Request.Context(), pubkey, int64(len(result.Data))); err != nil {
				if errors.Is(err, core.ErrQuotaExceeded) {
					// Preserve 402 Payment Required: clients use this to trigger
					// the BUD-07 payment/upgrade flow.
					clerrors.New(clerrors.CodeQuotaExceeded, "storage quota exceeded", http.StatusPaymentRequired).Abort(ctx)
					return
				}
				if errors.Is(err, core.ErrUserBanned) {
					clerrors.Forbidden(clerrors.CodeAccessDenied, "user is banned").Abort(ctx)
					return
				}
				clerrors.InternalError(clerrors.CodeInternalError, "failed to check quota").Abort(ctx)
				return
			}
		}

		// Calculate hash of original for verification
		originalHash := sha256.Sum256(bodyBytes)
		originalHashStr := hex.EncodeToString(originalHash[:])

		// Check if x tag matches (if provided in auth)
		if xTag := ctx.GetString("x"); xTag != "" && xTag != originalHashStr {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "x tag does not match uploaded content hash").Abort(ctx)
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
			clerrors.InternalError(clerrors.CodeInternalError, fmt.Sprintf("failed to save processed media: %s", err.Error())).Abort(ctx)
			return
		}

		// Honor the requested expiration (best-effort; blob is already stored).
		applyUploadExpiration(ctx, services, result.Hash, expiresAt, hasExpiration)

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
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash is required").Abort(ctx)
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
			clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found or not an image").Abort(ctx)
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
