package gin

import (
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"net/http"
	"net/url"

	bud04 "git.aegis-hq.xyz/coldforge/cloistr-blossom/src/bud-04"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/blossom"
	"github.com/gin-gonic/gin"
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
				clerrors.Forbidden(clerrors.CodeAccessDenied, "your account has been blocked due to terms of service violation").Abort(ctx)
				return
			}
		}

		if pubkey == "" {
			clerrors.InternalError(clerrors.CodeInternalError, "pubkey missing from context").Abort(ctx)
			return
		}

		if authSha256 == "" {
			clerrors.InternalError(clerrors.CodeInternalError, "blob hash missing from context").Abort(ctx)
			return
		}

		mirrorInput := &mirrorInput{}
		if err := ctx.ShouldBindJSON(mirrorInput); err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "invalid request body").Abort(ctx)
			return
		}

		// BUD-10: Support blossom: URI scheme
		var blobUrl *url.URL
		var blossomURI *blossom.URI
		if blossom.IsBlossom(mirrorInput.Url) {
			var err error
			blossomURI, err = blossom.Parse(mirrorInput.Url)
			if err != nil {
				clerrors.BadRequest(clerrors.CodeInvalidFormat, "invalid blossom URI: "+err.Error()).Abort(ctx)
				return
			}
			// Validate that auth hash matches URI hash
			if authSha256 != "" && authSha256 != blossomURI.Hash {
				clerrors.BadRequest(clerrors.CodeInvalidInput, "auth hash does not match blossom URI hash").Abort(ctx)
				return
			}
			// Use first server hint as URL
			httpURLs := blossomURI.ToHTTPURLs()
			if len(httpURLs) == 0 {
				clerrors.BadRequest(clerrors.CodeInvalidInput, "blossom URI has no server hints").Abort(ctx)
				return
			}
			blobUrl, err = url.Parse(httpURLs[0])
			if err != nil {
				clerrors.BadRequest(clerrors.CodeInvalidFormat, "invalid server URL from blossom URI").Abort(ctx)
				return
			}
		} else {
			var err error
			blobUrl, err = url.Parse(mirrorInput.Url)
			if err != nil {
				clerrors.BadRequest(clerrors.CodeInvalidFormat, "invalid blob URL").Abort(ctx)
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
			clerrors.BadRequest(clerrors.CodeInvalidInput, err.Error()).Abort(ctx)
			return
		}

		ctx.JSON(
			http.StatusOK,
			fromDomainBlobDescriptor(blobDescriptor),
		)
	}
}
