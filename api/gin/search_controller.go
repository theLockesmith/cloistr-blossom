package gin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

// searchMaxLimit caps the page size for a single search request.
const searchMaxLimit = 200

// searchBlobs handles GET /admin/api/blobs/search for server-wide blob search.
//
// It is registered behind admin session auth: server-wide enumeration is a
// moderation/ops tool, not a public endpoint (that would overlap with, and be
// better served by, Nostr relay queries). Supported query parameters:
//
//	type    MIME type prefix, e.g. "image/" or "video/mp4"
//	pubkey  restrict to a single uploader (hex)
//	since   created >= this Unix timestamp
//	until   created <= this Unix timestamp
//	min_size, max_size  size bounds in bytes
//	limit   page size (default 50, max 200)
//	offset  pagination offset
//	sort    "desc" for newest-first (default ascending)
func searchBlobs(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		filter := parseSearchFilter(ctx)

		result, err := services.Blob().SearchBlobs(ctx.Request.Context(), filter)
		if err != nil {
			clerrors.InternalError(clerrors.CodeInternalError, "failed to search blobs").Abort(ctx)
			return
		}

		ctx.JSON(http.StatusOK, blobListResponse{
			Blobs: fromSliceDomainBlobDescriptor(result.Blobs),
			Total: result.Total,
		})
	}
}

// parseSearchFilter builds a BlobFilter from search query parameters, always
// applying a bounded limit so a search can never return an unbounded result set.
func parseSearchFilter(ctx *gin.Context) *core.BlobFilter {
	filter := &core.BlobFilter{
		TypePrefix: ctx.Query("type"),
		Pubkey:     ctx.Query("pubkey"),
	}

	if v, err := strconv.ParseInt(ctx.Query("since"), 10, 64); err == nil && v > 0 {
		filter.Since = v
	}
	if v, err := strconv.ParseInt(ctx.Query("until"), 10, 64); err == nil && v > 0 {
		filter.Until = v
	}
	if v, err := strconv.ParseInt(ctx.Query("min_size"), 10, 64); err == nil && v > 0 {
		filter.MinSize = v
	}
	if v, err := strconv.ParseInt(ctx.Query("max_size"), 10, 64); err == nil && v > 0 {
		filter.MaxSize = v
	}
	if v, err := strconv.Atoi(ctx.Query("offset")); err == nil && v > 0 {
		filter.Offset = v
	}
	if ctx.Query("sort") == "desc" {
		filter.SortDesc = true
	}

	// Bounded page size: default 50, capped at searchMaxLimit.
	filter.Limit = 50
	if v, err := strconv.Atoi(ctx.Query("limit")); err == nil && v > 0 {
		filter.Limit = v
	}
	if filter.Limit > searchMaxLimit {
		filter.Limit = searchMaxLimit
	}

	return filter
}
