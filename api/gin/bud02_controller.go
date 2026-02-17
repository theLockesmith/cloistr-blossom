package gin

import (
	"fmt"
	"io"
	"net/http"

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
		blobs, err := bud02.ListBlobs(
			ctx.Request.Context(),
			services,
			ctx.Param("pubkey"),
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
