package gin

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	bud02 "git.coldforge.xyz/coldforge/cloistr-blossom/src/bud-02"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
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
				ctx.AbortWithStatusJSON(
					http.StatusForbidden,
					apiError{Message: "your account has been blocked due to terms of service violation"},
				)
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
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: fmt.Sprintf("failed to read request body: %s", err.Error()),
				},
			)
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
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: fmt.Sprintf("%s", err.Error()),
				},
			)
			return
		}

		// Record successful upload metrics
		metrics.UploadsTotal.WithLabelValues("success", string(blobDescriptor.EncryptionMode)).Inc()
		metrics.UploadBytes.Add(float64(len(bodyBytes)))

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
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{Message: err.Error()},
				)
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
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: err.Error(),
				},
			)
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
			ctx.Param("path"),
			ctx.GetString("x"),
		); err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: err.Error(),
				},
			)
			return
		}

		ctx.Status(http.StatusOK)
	}
}
