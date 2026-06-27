package gin

import (
	"encoding/hex"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"net/http"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"github.com/gin-gonic/gin"
)

// isValidHexPubkey validates that a string is a 64-character hex-encoded pubkey.
func isValidHexPubkey(pubkey string) bool {
	if len(pubkey) != 64 {
		return false
	}
	_, err := hex.DecodeString(pubkey)
	return err == nil
}

// getUserServerList returns a user's Blossom server list from their kind 10063 event.
// BUD-03: User Server List
func getUserServerList(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pubkey := ctx.Param("pubkey")

		// Validate pubkey format (64 hex chars)
		if !isValidHexPubkey(pubkey) {
			clerrors.BadRequest(clerrors.CodeInvalidPubkey, "invalid pubkey format").Abort(ctx)
			return
		}

		// Check if federation service is available
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, "server list discovery not available", 0).Abort(ctx)
			return
		}

		servers, err := federation.GetUserServerList(ctx.Request.Context(), pubkey)
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, "failed to get server list").Abort(ctx)
			return
		}

		if len(servers) == 0 {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "no server list found for pubkey").Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"pubkey":  pubkey,
			"servers": servers,
		})
	}
}
