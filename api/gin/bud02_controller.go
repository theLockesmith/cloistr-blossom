package gin

import (
	"fmt"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"io"
	"net/http"
	"strconv"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	bud02 "git.aegis-hq.xyz/coldforge/cloistr-blossom/src/bud-02"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/hashing"
	"github.com/gin-gonic/gin"
)

func uploadBlob(
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

		bodyBytes, err := io.ReadAll(ctx.Request.Body)
		defer func(body io.ReadCloser) {
			err := body.Close()
			if err != nil {

			}
		}(ctx.Request.Body)
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, fmt.Sprintf("failed to read request body: %s", err.Error())).Abort(ctx)
			return
		}

		// Determine encryption mode from header
		// Valid values: "none", "server", "e2e"
		// Default to "none" which will use server encryption if enabled
		encryptionMode := core.EncryptionModeNone
		if encHeader := ctx.GetHeader("X-Encryption"); encHeader != "" {
			switch encHeader {
			case "server":
				encryptionMode = core.EncryptionModeServer
			case "e2e":
				encryptionMode = core.EncryptionModeE2E
			case "none":
				encryptionMode = core.EncryptionModeNone
			default:
				clerrors.BadRequest(clerrors.CodeInvalidInput, "invalid encryption mode: valid values are none, server, e2e").Abort(ctx)
				return
			}
		}

		// Validate optional X-Expiration header before storing anything so a
		// malformed value never leaves an orphaned blob behind.
		expiresAt, hasExpiration, ok := parseUploadExpiration(ctx)
		if !ok {
			return
		}

		// AI content moderation scanning
		if aiMod := services.AIModeration(); aiMod != nil && aiMod.IsEnabled() {
			mimeType := ctx.GetHeader("Content-Type")
			if aiMod.ShouldScan(mimeType, int64(len(bodyBytes))) {
				hash, _ := hashing.Hash(bodyBytes)
				scanReq := &core.ScanRequest{
					Hash:     hash,
					Data:     bodyBytes,
					MimeType: mimeType,
					Size:     int64(len(bodyBytes)),
					Pubkey:   pubkey,
				}

				result, err := aiMod.ScanContent(ctx.Request.Context(), scanReq)
				if err == nil {
					switch result.RecommendedAction {
					case core.ScanActionBlock:
						metrics.BlockedUploadsTotal.Inc()
						clerrors.Forbidden(clerrors.CodeAccessDenied, "content blocked by automated moderation").Abort(ctx)
						return
					case core.ScanActionQuarantine:
						// Quarantine the content for review
						_ = aiMod.QuarantineBlob(ctx.Request.Context(), hash, pubkey, result)
						ctx.AbortWithStatusJSON(
							http.StatusAccepted,
							gin.H{
								"message": "content is pending review",
								"status":  "quarantined",
								"hash":    hash,
							},
						)
						return
					case core.ScanActionFlag:
						// Allow but create a report for human review
						// The upload will continue normally
					}
				}
			}
		}

		blobDescriptor, err := bud02.UploadBlob(
			ctx.Request.Context(),
			services,
			cdnBaseUrl,
			ctx.GetString("x"),
			ctx.GetString("pk"),
			bodyBytes,
			encryptionMode,
		)
		if err != nil {
			metrics.UploadsTotal.WithLabelValues("error", string(encryptionMode)).Inc()
			clerrors.BadRequest(clerrors.CodeUploadFailed, err.Error()).Abort(ctx)
			return
		}

		// Record successful upload metrics
		metrics.UploadsTotal.WithLabelValues("success", string(blobDescriptor.EncryptionMode)).Inc()
		metrics.UploadBytes.Add(float64(len(bodyBytes)))

		// Honor the requested expiration (best-effort; blob is already stored).
		applyUploadExpiration(ctx, services, blobDescriptor.Sha256, expiresAt, hasExpiration)

		// Publish to federation if enabled (async, non-blocking)
		if federation := services.Federation(); federation != nil && federation.IsEnabled() {
			go federation.PublishBlobAsync(ctx.Request.Context(), blobDescriptor)
		}

		ctx.JSON(
			http.StatusOK,
			fromDomainBlobDescriptor(blobDescriptor),
		)
	}
}

func listBlobs(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		// Parse filter parameters from query string
		filter := parseBlobFilter(ctx)

		// Use filtered query if any filters are specified
		if filter != nil {
			result, err := services.Blob().GetFromPubkeyWithFilter(
				ctx.Request.Context(),
				pubkey,
				filter,
			)
			if err != nil {
				clerrors.BadRequest(clerrors.CodeInvalidInput, err.Error()).Abort(ctx)
				return
			}

			// Return response with pagination info
			ctx.JSON(http.StatusOK, blobListResponse{
				Blobs: fromSliceDomainBlobDescriptor(result.Blobs),
				Total: result.Total,
			})
			return
		}

		// Fall back to original behavior for backwards compatibility
		blobs, err := bud02.ListBlobs(
			ctx.Request.Context(),
			services,
			pubkey,
		)
		if err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, err.Error()).Abort(ctx)
			return
		}

		ctx.JSON(
			http.StatusOK,
			fromSliceDomainBlobDescriptor(blobs),
		)
	}
}

// blobListResponse is the response for filtered blob listings.
type blobListResponse struct {
	Blobs []*blobDescriptor `json:"blobs"`
	Total int64             `json:"total"`
}

// parseBlobFilter extracts filter parameters from query string.
// Returns nil if no filter parameters are specified.
func parseBlobFilter(ctx *gin.Context) *core.BlobFilter {
	filter := &core.BlobFilter{}
	hasFilter := false

	// Type prefix filter (e.g., "image/", "video/", "application/pdf")
	if t := ctx.Query("type"); t != "" {
		filter.TypePrefix = t
		hasFilter = true
	}

	// Since timestamp filter
	if since := ctx.Query("since"); since != "" {
		if ts, err := strconv.ParseInt(since, 10, 64); err == nil && ts > 0 {
			filter.Since = ts
			hasFilter = true
		}
	}

	// Until timestamp filter
	if until := ctx.Query("until"); until != "" {
		if ts, err := strconv.ParseInt(until, 10, 64); err == nil && ts > 0 {
			filter.Until = ts
			hasFilter = true
		}
	}

	// Limit for pagination
	if limit := ctx.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 && l <= 1000 {
			filter.Limit = l
			hasFilter = true
		}
	}

	// Offset for pagination
	if offset := ctx.Query("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			filter.Offset = o
			hasFilter = true
		}
	}

	// Sort order (default: ascending by created)
	if sort := ctx.Query("sort"); sort == "desc" {
		filter.SortDesc = true
		hasFilter = true
	}

	if !hasFilter {
		return nil
	}

	return filter
}

func deleteBlob(
	services core.Services,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if err := bud02.DeleteBlob(
			ctx.Request.Context(),
			services,
			ctx.GetString("pk"),
			ctx.Param("hash"),
			ctx.GetString("x"),
		); err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, err.Error()).Abort(ctx)
			return
		}

		ctx.Status(http.StatusOK)
	}
}
