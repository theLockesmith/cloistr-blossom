package gin

import (
	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

const (
	blockedUploaderMessage      = "your account has been blocked due to terms of service violation"
	blocklistUnavailableMessage = "could not check the blocklist; try again"
)

// refuseBlockedUploader answers the request and returns true when pubkey may
// not upload: it is on the blocklist (403), or the blocklist could not be
// checked (503). A lookup error refuses because a refused upload can be
// retried, while a banned key's stored content cannot be taken back.
func refuseBlockedUploader(ctx *gin.Context, moderation core.ModerationService, pubkey string) bool {
	if moderation == nil {
		return false
	}
	blocked, err := moderation.IsBlocked(ctx.Request.Context(), pubkey)
	if err != nil {
		clerrors.ServiceUnavailable(clerrors.CodeServiceUnavailable, blocklistUnavailableMessage, 30).Abort(ctx)
		return true
	}
	if blocked {
		metrics.BlockedUploadsTotal.Inc()
		clerrors.Forbidden(clerrors.CodeAccessDenied, blockedUploaderMessage).Abort(ctx)
		return true
	}
	return false
}
