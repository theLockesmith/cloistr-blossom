package gin

import (
	"errors"
	"net/http"
	"strconv"

	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/urlsign"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// Error codes specific to the media mirror. They exist as distinct codes
// because the client has to render three different things:
//
//	MIRROR_REFUSED     -- we looked and said no. Permanent. Show a "not
//	                      available" placeholder; do not retry.
//	MIRROR_UNREACHABLE -- the remote host did not answer. Transient. A retry
//	                      later may work.
//	MIRROR_UNSIGNED    -- the link is not one of ours, or has expired. The app
//	                      should re-sign, not retry the same link.
//
// A single generic error for all three is what produces the "it's just
// broken" bug report that nobody can act on.
const (
	CodeMirrorRefused     = "MIRROR_REFUSED"
	CodeMirrorUnreachable = "MIRROR_UNREACHABLE"
	CodeMirrorUnsigned    = "MIRROR_UNSIGNED"
	CodeMirrorDisabled    = "MIRROR_DISABLED"
)

// registerMediaMirrorRoutes mounts the mirror endpoints.
//
// Both routes are registered UNCONDITIONALLY, even when the mirror is disabled,
// and answer 501 in that state.
//
// They used to be registered only when enabled, which meant a disabled mirror
// returned 404 -- indistinguishable from a typo in the path, a missing ingress
// rule, or a server too old to have the feature. That cost real time: the
// endpoint 404'd in production and was read as a failed deploy, when the new
// build was in fact running perfectly and the feature simply was not switched
// on. Nothing in a 404 can tell you which of those it is.
//
// A route that answers "this server has the feature and it is off" is
// diagnosable. A route that is absent is not. So the enabled-check is a handler
// concern, not a registration concern.
//
// This lives in its own function so a test can assert the routes exist while
// disabled; asserting that against SetupRoutes would require standing up every
// other service the router touches.
func registerMediaMirrorRoutes(r gin.IRouter, services core.Services, log *zap.Logger) {
	// requireMediaMirror runs BEFORE the auth middleware on the sign route on
	// purpose: whether the feature exists is not a secret, and making an
	// unauthenticated caller see 401 instead of 501 would hide the actual state
	// behind a login they cannot complete.
	r.POST(
		service.MirrorRoutePath+"/sign",
		requireMediaMirror(services),
		nostrAuthMiddleware("mirror", log),
		signMirrorURLs(services, log),
	)
	r.GET(
		service.MirrorRoutePath,
		requireMediaMirror(services),
		getMirroredMedia(services, log),
	)

	if services.MediaMirror() != nil && services.MediaMirror().IsEnabled() {
		log.Info("media mirror routes registered and enabled")
	} else {
		log.Info("media mirror routes registered but DISABLED — they will answer 501 " +
			"until media_mirror.enabled and media_mirror.signing_key are set")
	}
}

// requireMediaMirror short-circuits with 501 when the mirror is not enabled.
//
// This is middleware rather than a check inside each handler so it can run
// BEFORE authentication on the sign route. Whether a server has a feature
// switched on is not a secret, and answering 401 to an unauthenticated caller
// would hide "not enabled" behind a login they cannot complete -- leaving them
// to guess, which is the failure mode this whole change exists to remove.
//
// 501 Not Implemented, not 404: the route exists and the server understands it.
// It is simply turned off. A client can and should treat this as "degrade
// gracefully to no remote images" rather than "something is broken".
func requireMediaMirror(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		mirror := services.MediaMirror()
		if mirror == nil || !mirror.IsEnabled() {
			clerrors.New(CodeMirrorDisabled, "media mirroring is not enabled on this server",
				http.StatusNotImplemented).Abort(ctx)
			return
		}
		ctx.Next()
	}
}

// signMirrorInput is the request body for minting mirror links.
type signMirrorInput struct {
	URLs []string `json:"urls"`
}

// signMirrorOutput returns the links that were minted and the inputs that were
// not, so a batch containing one broken URL still yields the rest.
type signMirrorOutput struct {
	Signed   []core.SignedMirrorURL   `json:"signed"`
	Rejected []core.RejectedMirrorURL `json:"rejected"`
}

// signMirrorURLs mints mirror links for remote media URLs.
//
// WHY THIS ENDPOINT EXISTS AT ALL. The signing key cannot live in the client:
// our apps are static single-page bundles, so anything they hold is readable
// by anyone who opens devtools, and a readable signing key means an open
// proxy. So signing happens here, behind Nostr auth, and the app exchanges a
// list of URLs for a list of signed links.
//
// WHY THIS DOES NOT REINTRODUCE TRACKING. The signature covers the URL and
// nothing else -- it is identical for every user, so it cannot be used to tell
// them apart, and the request that mints it is not recorded. Signing is also
// decoupled in time from viewing: an app signs a whole emoji set once when it
// loads it, and the images are fetched later on the unauthenticated route. The
// two cannot be correlated, which is the property that lets the fetch stay
// anonymous while the mint stays bounded to our users.
func signMirrorURLs(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// requireMediaMirror has already rejected the disabled case; this
		// repeats it so the handler is safe if it is ever mounted without
		// that middleware.
		mirror := services.MediaMirror()
		if mirror == nil || !mirror.IsEnabled() {
			clerrors.New(CodeMirrorDisabled, "media mirroring is not enabled on this server",
				http.StatusNotImplemented).Abort(ctx)
			return
		}

		input := &signMirrorInput{}
		if err := ctx.ShouldBindJSON(input); err != nil {
			clerrors.BadRequest(clerrors.CodeInvalidInput, "invalid request body").Abort(ctx)
			return
		}
		if len(input.URLs) == 0 {
			clerrors.BadRequest(clerrors.CodeMissingField, "urls must not be empty").Abort(ctx)
			return
		}

		signed, rejected, err := mirror.SignURLs(ctx.Request.Context(), input.URLs)
		if err != nil {
			if me, ok := core.AsMirrorError(err); ok {
				clerrors.BadRequest(clerrors.CodeInvalidInput, me.Reason).Abort(ctx)
				return
			}
			log.Warn("media mirror: signing failed", zap.Error(err))
			clerrors.InternalError(clerrors.CodeInternalError, "could not sign urls").Abort(ctx)
			return
		}

		// Deliberately nothing is logged about WHICH urls were signed or by
		// whom. The pubkey is available in the gin context here; recording it
		// next to these URLs is exactly the surveillance this feature removes.
		ctx.JSON(http.StatusOK, signMirrorOutput{Signed: signed, Rejected: rejected})
	}
}

// getMirroredMedia serves a mirrored remote image.
//
// Unauthenticated by design. This is the request a browser makes for every
// <img src> on a page, and requiring auth here would attach an identity to
// every image view -- the precise thing the mirror exists to prevent. Abuse is
// bounded by the signature (only our apps mint links), by the caps in the
// service, and by the existing rate-limit middleware.
//
// This route is also excluded from the access log; see SetupRoutes.
func getMirroredMedia(services core.Services, log *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// Defence in depth; see the note in signMirrorURLs.
		mirror := services.MediaMirror()
		if mirror == nil || !mirror.IsEnabled() {
			clerrors.New(CodeMirrorDisabled, "media mirroring is not enabled on this server",
				http.StatusNotImplemented).Abort(ctx)
			return
		}

		encodedURL := ctx.Query("u")
		signature := ctx.Query("s")
		if encodedURL == "" || signature == "" {
			// Same code and status as a bad signature. An unsigned request and
			// a wrongly-signed one are the same thing from here -- neither is
			// ours -- and distinguishing them only helps someone probing.
			clerrors.Forbidden(CodeMirrorUnsigned, "link is not signed by this server").Abort(ctx)
			return
		}

		var expiresAt int64
		if raw := ctx.Query("e"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				clerrors.Forbidden(CodeMirrorUnsigned, "link is not signed by this server").Abort(ctx)
				return
			}
			expiresAt = parsed
		}

		media, err := mirror.VerifyAndGet(ctx.Request.Context(), encodedURL, expiresAt, signature)
		if err != nil {
			writeMirrorError(ctx, log, err)
			return
		}

		// Immutable and content-addressed, so it can be cached hard. The
		// mirrored bytes for a given signed link never change: a different
		// image means a different URL means a different signature.
		ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
		// The blob hash, so a client that wants the canonical Blossom URL for
		// this content can go straight to GET /<hash> and skip the mirror
		// entirely from then on.
		ctx.Header("X-Blossom-Sha256", media.Sha256)
		// Whether this request paid for a remote fetch. Useful when debugging
		// mirror behaviour, and says nothing about who asked.
		if media.FromCache {
			ctx.Header("X-Mirror-Status", "hit")
		} else {
			ctx.Header("X-Mirror-Status", "miss")
		}
		// The mirror serves images fetched from hosts we do not control. CSP
		// sandbox plus nosniff means that even if a malicious payload survived
		// the magic-byte check, the browser will not execute it or reinterpret
		// its type.
		ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
		ctx.Header("X-Content-Type-Options", "nosniff")
		// Do not let the image's own URL leak onward if the browser follows
		// anything from this response.
		ctx.Header("Referrer-Policy", "no-referrer")

		ctx.Data(http.StatusOK, media.Mime, media.Data)
	}
}

// writeMirrorError maps a mirror failure onto a status the client can act on.
//
// The three-way split is the contract: 403 means re-sign, 502 means retry
// later, 415 means never retry. Detail is deliberately omitted from the
// response body -- the service logs it, but returning it would let anyone
// holding a signed link use the mirror to probe remote hosts and read the
// answers.
func writeMirrorError(ctx *gin.Context, log *zap.Logger, err error) {
	switch {
	case errors.Is(err, urlsign.ErrBadSignature),
		errors.Is(err, urlsign.ErrExpired),
		errors.Is(err, urlsign.ErrMalformed):
		clerrors.Forbidden(CodeMirrorUnsigned, "link is not signed by this server").Abort(ctx)
		return
	case errors.Is(err, core.ErrMirrorDisabled):
		clerrors.New(CodeMirrorDisabled, "media mirroring is not enabled on this server",
			http.StatusNotImplemented).Abort(ctx)
		return
	}

	me, ok := core.AsMirrorError(err)
	if !ok {
		log.Warn("media mirror: unclassified failure", zap.Error(err))
		clerrors.InternalError(clerrors.CodeInternalError, "mirror failed").Abort(ctx)
		return
	}

	switch me.Status {
	case core.MirrorStatusRefused:
		// 415 rather than 403: the request was legitimate, the CONTENT was
		// not. A client that sees 415 knows re-signing will not help.
		clerrors.New(CodeMirrorRefused, me.Reason, http.StatusUnsupportedMediaType).Abort(ctx)
	case core.MirrorStatusUnreachable:
		// 502: we are a gateway and the upstream failed. Retry-After keeps a
		// client from hammering a host that is already down, and matches the
		// negative-cache window during which we would answer from cache anyway.
		clerrors.New(CodeMirrorUnreachable, me.Reason, http.StatusBadGateway).
			WithRetryAfter(mirrorRetryAfterSeconds).Abort(ctx)
	default:
		clerrors.InternalError(clerrors.CodeInternalError, "mirror failed").Abort(ctx)
	}
}

// mirrorRetryAfterSeconds is advertised on an unreachable response. It matches
// the default negative-cache TTL: retrying sooner would be answered from the
// cached failure anyway, so telling the client to wait is honest rather than
// merely defensive.
const mirrorRetryAfterSeconds = 900
