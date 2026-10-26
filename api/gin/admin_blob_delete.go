package gin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	bud02 "git.aegis-hq.xyz/coldforge/cloistr-blossom/src/bud-02"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

// adminDeleteBlob handles DELETE /admin/api/blobs/:hash.
//
// It removes a blob for every owner and refunds each owner's usage, without
// adding the hash to the removed-blobs list (use a report's "removed" action
// for content that must stay blocked). Like gc/reconcile it is a dry run
// unless called with ?confirm=true; the dry run lists the owners it would
// touch.
func adminDeleteBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := strings.ToLower(ctx.Param("hash"))
		if !sha256Regex.MatchString(hash) {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "hash must be 64 hex characters").Abort(ctx)
			return
		}

		dryRun := ctx.Query("confirm") != "true"
		res, err := bud02.AdminDeleteBlob(ctx.Request.Context(), services, hash, dryRun)
		if errors.Is(err, core.ErrBlobNotFound) {
			clerrors.NotFound(clerrors.CodeResourceNotFound, "blob not found").Abort(ctx)
			return
		}
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, "failed to delete blob").Abort(ctx)
			return
		}
		ctx.JSON(http.StatusOK, res)
	}
}
