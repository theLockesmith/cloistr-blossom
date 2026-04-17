package gin

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	bud04 "git.coldforge.xyz/coldforge/cloistr-blossom/src/bud-04"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/blossom"
)

func mirrorBlob(
	services core.Services,
	cdnBaseUrl string,
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.GetString("pk")
		authSha256 := ctx.GetString("x")

		// Check if pubkey is blocked
		if pubkey != "" {
			isBlocked, err := services.Moderation().IsBlocked(ctx.Request.Context(), pubkey)
			if err == nil && isBlocked {
				ctx.AbortWithStatusJSON(
					http.StatusForbidden,
					apiError{Message: "your account has been blocked due to terms of service violation"},
				)
				return
			}
		}

		if pubkey == "" {
			ctx.AbortWithStatusJSON(
				http.StatusInternalServerError,
				apiError{
					Message: "pubkey missing from context",
				},
			)
			return
		}

		if authSha256 == "" {
			ctx.AbortWithStatusJSON(
				http.StatusInternalServerError,
				apiError{
					Message: "blob hash missing from context",
				},
			)
			return
		}

		mirrorInput := &mirrorInput{}
		if err := ctx.ShouldBindJSON(mirrorInput); err != nil {
			ctx.AbortWithStatusJSON(
				http.StatusBadRequest,
				apiError{
					Message: "invalid request body",
				},
			)
			return
		}

		// BUD-10: Support blossom: URI scheme
		var blobUrl *url.URL
		var blossomURI *blossom.URI
		if blossom.IsBlossom(mirrorInput.Url) {
			var err error
			blossomURI, err = blossom.Parse(mirrorInput.Url)
			if err != nil {
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{
						Message: "invalid blossom URI: " + err.Error(),
					},
				)
				return
			}
			// Validate that auth hash matches URI hash
			if authSha256 != "" && authSha256 != blossomURI.Hash {
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{
						Message: "auth hash does not match blossom URI hash",
					},
				)
				return
			}
			// Use first server hint as URL
			httpURLs := blossomURI.ToHTTPURLs()
			if len(httpURLs) == 0 {
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{
						Message: "blossom URI has no server hints",
					},
				)
				return
			}
			blobUrl, err = url.Parse(httpURLs[0])
			if err != nil {
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{
						Message: "invalid server URL from blossom URI",
					},
				)
				return
			}
		} else {
			var err error
			blobUrl, err = url.Parse(mirrorInput.Url)
			if err != nil {
				ctx.AbortWithStatusJSON(
					http.StatusBadRequest,
					apiError{
						Message: "invalid blob URL",
					},
				)
				return
			}
		}
		_ = blossomURI // May be used for future server fallback logic

		// Determine encryption mode from header
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

		blobDescriptor, err := bud04.MirrorBlob(
			ctx,
			services,
			cdnBaseUrl,
			pubkey,
			authSha256,
			*blobUrl,
			encryptionMode,
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
			fromDomainBlobDescriptor(blobDescriptor),
		)
	}
}
