package gin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
)

// expirationHeader is the request header clients use to request that an uploaded
// blob be auto-deleted. Its value is a NIP-40 style absolute Unix timestamp (in
// seconds) at which the blob expires. This matches the semantics of the
// blobs.expires_at column and the Nostr `expiration` tag.
const expirationHeader = "X-Expiration"

// parseUploadExpiration validates the optional X-Expiration request header.
//
// It returns (expiresAt, hasExpiration, ok):
//   - a missing/empty header is a no-op: (zero, false, true)
//   - a malformed header (non-integer, non-positive, or in the past) writes a
//     400 response and returns ok=false; the caller must stop processing.
//
// Validation runs before the blob is stored so client errors never leave an
// orphaned blob behind.
func parseUploadExpiration(ctx *gin.Context) (expiresAt time.Time, hasExpiration bool, ok bool) {
	header := ctx.GetHeader(expirationHeader)
	if header == "" {
		return time.Time{}, false, true
	}

	ts, err := strconv.ParseInt(header, 10, 64)
	if err != nil || ts <= 0 {
		clerrors.BadRequest(clerrors.CodeInvalidInput,
			"invalid X-Expiration header: expected an absolute Unix timestamp in seconds").Abort(ctx)
		return time.Time{}, false, false
	}

	expiresAt = time.Unix(ts, 0)
	if !expiresAt.After(time.Now()) {
		clerrors.BadRequest(clerrors.CodeInvalidInput,
			"invalid X-Expiration header: timestamp must be in the future").Abort(ctx)
		return time.Time{}, false, false
	}

	return expiresAt, true, true
}

// applyUploadExpiration records the requested expiration for an already-stored
// blob. It is best-effort: the blob is already persisted, so a storage failure
// is logged via metrics rather than failing the upload. On success it echoes the
// applied expiration back in the X-Expiration response header so clients can
// confirm it was honored. Pass the value returned by parseUploadExpiration.
func applyUploadExpiration(ctx *gin.Context, services core.Services, hash string, expiresAt time.Time, hasExpiration bool) {
	if !hasExpiration {
		return
	}

	if err := services.Expiration().SetExpiration(ctx.Request.Context(), hash, expiresAt); err != nil {
		metrics.ErrorsTotal.WithLabelValues("set_expiration").Inc()
		return
	}

	ctx.Header(expirationHeader, strconv.FormatInt(expiresAt.Unix(), 10))
}
