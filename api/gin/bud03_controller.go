package gin

import (
	"encoding/hex"
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
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: "invalid pubkey format",
			})
			return
		}

		// Check if federation service is available
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, apiError{
				Message: "server list discovery not available",
			})
			return
		}

		servers, err := federation.GetUserServerList(ctx.Request.Context(), pubkey)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to get server list",
			})
			return
		}

		if len(servers) == 0 {
			ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{
				Message: "no server list found for pubkey",
			})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"pubkey":  pubkey,
			"servers": servers,
		})
	}
}
