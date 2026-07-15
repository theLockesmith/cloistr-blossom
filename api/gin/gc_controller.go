package gin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

// gcReconcileDefaultLimit bounds a reconcile run when no limit is supplied.
const gcReconcileDefaultLimit = 1000

// gcReport handles GET /admin/api/gc/report.
//
// Read-only: it returns the current reference-bookkeeping drift counts
// (zero-ref and ownerless blobs) and refreshes the corresponding Prometheus
// gauges. It never deletes anything.
func gcReport(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		report, err := services.GC().Report(ctx.Request.Context())
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, "failed to build gc report").Abort(ctx)
			return
		}
		ctx.JSON(http.StatusOK, report)
	}
}

// gcReconcile handles POST /admin/api/gc/reconcile.
//
// This is the manual, operator-triggered cleanup of ownerless blobs. It is
// dry-run by DEFAULT: it only deletes when called with ?confirm=true, so an
// accidental invocation reports what would be removed without removing it.
// Scheduled auto-deletion is intentionally not wired up yet.
//
// Query parameters:
//
//	confirm  "true" to actually delete; anything else (or absent) is a dry run
//	limit    max blobs to process this run (default 1000, capped by the service)
func gcReconcile(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		dryRun, limit := parseReconcileParams(ctx)

		result, err := services.GC().Reconcile(ctx.Request.Context(), dryRun, limit)
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, "failed to reconcile orphaned blobs").Abort(ctx)
			return
		}
		ctx.JSON(http.StatusOK, result)
	}
}

// parseReconcileParams derives the reconcile mode from the request. Deletion is
// off unless the caller explicitly opts in with ?confirm=true, so any other
// value — including a typo or an absent parameter — is a safe dry run.
func parseReconcileParams(ctx *gin.Context) (dryRun bool, limit int) {
	dryRun = ctx.Query("confirm") != "true"

	limit = gcReconcileDefaultLimit
	if v, err := strconv.Atoi(ctx.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	return dryRun, limit
}
