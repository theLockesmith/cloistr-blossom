package gin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// RateLimitMiddleware creates a rate limiting middleware.
func RateLimitMiddleware(
	limiter ratelimit.RateLimiter,
	conf *config.RateLimitingConfig,
	log *zap.Logger,
) gin.HandlerFunc {
	// Parse window durations at startup
	ipDownloadWindow := parseDuration(conf.IP.Download.Window, time.Minute)
	ipUploadWindow := parseDuration(conf.IP.Upload.Window, time.Minute)
	ipGeneralWindow := parseDuration(conf.IP.General.Window, time.Minute)
	pubkeyDownloadWindow := parseDuration(conf.Pubkey.Download.Window, time.Minute)
	pubkeyUploadWindow := parseDuration(conf.Pubkey.Upload.Window, time.Minute)
	pubkeyGeneralWindow := parseDuration(conf.Pubkey.General.Window, time.Minute)

	// Build whitelist set for fast lookup
	whitelist := make(map[string]bool)
	for _, pk := range conf.WhitelistedPubkeys {
		whitelist[pk] = true
	}

	return func(c *gin.Context) {
		if !conf.Enabled || (!conf.LimitInternalCallers && isInternalCaller(c, conf.ClientIPHeader)) {
			c.Next()
			return
		}

		// Determine request type
		requestType := classifyRequest(c)

		ip := c.ClientIP()
		var ipLimit, pkLimit config.RateLimitConfig
		var ipWindow, pkWindow time.Duration
		switch requestType {
		case "download":
			ipLimit, ipWindow = conf.IP.Download, ipDownloadWindow
			pkLimit, pkWindow = conf.Pubkey.Download, pubkeyDownloadWindow
		case "upload":
			ipLimit, ipWindow = conf.IP.Upload, ipUploadWindow
			pkLimit, pkWindow = conf.Pubkey.Upload, pubkeyUploadWindow
		default:
			ipLimit, ipWindow = conf.IP.General, ipGeneralWindow
			pkLimit, pkWindow = conf.Pubkey.General, pubkeyGeneralWindow
		}

		// The IP limit comes first and always applies: minting fresh keys from
		// one address does not buy a fresh budget, and an address over its
		// limit is refused before any signature check, so junk auth headers
		// cannot buy Schnorr verifies. A whitelisted key's requests therefore
		// count toward its address's budget too.
		// Each request type has its own counter, so downloads and metadata
		// polling do not use up an address's upload allowance.
		if !checkRateLimit(c, limiter, log, "ip:"+ip+":"+requestType, requestType, ipLimit, ipWindow) {
			return
		}

		// This runs before the per-route auth middleware, so it verifies the
		// auth header itself; an unverified claim is never trusted. A
		// verified key is also held to its own limit unless whitelisted.
		pubkey := verifiedPubkey(c)
		if pubkey != "" && whitelist[pubkey] {
			c.Next()
			return
		}
		if pubkey != "" && !checkRateLimit(c, limiter, log, "pk:"+pubkey+":"+requestType, requestType, pkLimit, pkWindow) {
			return
		}

		c.Next()
	}
}

// checkRateLimit counts the request against key and answers 429 when the
// limit is exceeded. It reports whether the request may continue.
func checkRateLimit(c *gin.Context, limiter ratelimit.RateLimiter, log *zap.Logger, key, requestType string, limitConfig config.RateLimitConfig, window time.Duration) bool {
	// Check rate limit
	allowed, remaining, resetAt := limiter.Allow(c.Request.Context(), key, limitConfig.Requests, window)

	// Set rate limit headers
	c.Header("X-RateLimit-Limit", strconv.Itoa(limitConfig.Requests))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

	if !allowed {
		retryAfter := int(time.Until(resetAt).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))

		log.Debug("rate limit exceeded",
			zap.String("key", key),
			zap.String("type", requestType),
			zap.Int("limit", limitConfig.Requests))

		metrics.RateLimitedTotal.WithLabelValues(requestType).Inc()

		clerrors.TooManyRequests(clerrors.CodeRateLimitExceeded, "rate limit exceeded, please try again later", retryAfter).Abort(c)
		return false
	}
	return true
}

// limiterVerify is the signature check the limiters use; tests swap it to
// count calls.
var limiterVerify = verifyAuthHeader

// verifiedPubkey returns the signer of a valid kind-24242 Authorization
// header, or "" when there is none or it does not verify.
// The result is kept on the request so the bandwidth limiter does not verify
// the same signature again.
func verifiedPubkey(c *gin.Context) string {
	if v, ok := c.Get(ctxLimiterPubkey); ok {
		return v.(string)
	}
	pubkey := ""
	if h := c.GetHeader("Authorization"); h != "" {
		if ev, _, reason := limiterVerify(h); reason == "" {
			pubkey = ev.PubKey
		}
	}
	c.Set(ctxLimiterPubkey, pubkey)
	return pubkey
}

// ctxLimiterPubkey caches verifiedPubkey's result for the request.
const ctxLimiterPubkey = "limiter_verified_pk"

// BandwidthLimitMiddleware creates a bandwidth limiting middleware.
// This should be applied after the response is written to track actual bytes.
func BandwidthLimitMiddleware(
	limiter ratelimit.BandwidthLimiter,
	conf *config.RateLimitingConfig,
	log *zap.Logger,
) gin.HandlerFunc {
	downloadLimitBytes := int64(conf.Bandwidth.DownloadMBPerMinute) * 1024 * 1024
	uploadLimitBytes := int64(conf.Bandwidth.UploadMBPerMinute) * 1024 * 1024
	window := time.Minute

	// Build whitelist set
	whitelist := make(map[string]bool)
	for _, pk := range conf.WhitelistedPubkeys {
		whitelist[pk] = true
	}

	return func(c *gin.Context) {
		if !conf.Enabled || (!conf.LimitInternalCallers && isInternalCaller(c, conf.ClientIPHeader)) {
			c.Next()
			return
		}

		// Check whitelist against a verified signer only (see RateLimitMiddleware).
		if pk := verifiedPubkey(c); pk != "" && whitelist[pk] {
			c.Next()
			return
		}

		// Bandwidth is counted per IP: per-key buckets would let one address
		// mint keys for fresh budgets.
		key := "ip:" + c.ClientIP()

		// For uploads, check bandwidth before processing. PATCH carries tus
		// chunk bytes.
		if c.Request.Method == "PUT" || c.Request.Method == "POST" || c.Request.Method == "PATCH" {
			// charge counts n upload bytes and, once the address is over its
			// cap, answers 429 and reports false.
			charge := func(n int64) bool {
				allowed, remaining, resetAt := limiter.AllowBytes(
					c.Request.Context(),
					"upload:"+key,
					n,
					uploadLimitBytes,
					window,
				)
				if c.Writer.Written() {
					return allowed
				}

				c.Header("X-Bandwidth-Remaining", strconv.FormatInt(remaining, 10))
				c.Header("X-Bandwidth-Reset", strconv.FormatInt(resetAt.Unix(), 10))

				if !allowed {
					retryAfter := int(time.Until(resetAt).Seconds())
					if retryAfter < 1 {
						retryAfter = 1
					}
					c.Header("Retry-After", strconv.Itoa(retryAfter))

					log.Debug("bandwidth limit exceeded",
						zap.String("key", key),
						zap.String("type", "upload"),
						zap.Int64("bytes", n))

					metrics.RateLimitedTotal.WithLabelValues("bandwidth_upload").Inc()

					clerrors.TooManyRequests(clerrors.CodeRateLimitExceeded, "bandwidth limit exceeded, please try again later", retryAfter).Abort(c)
				}
				return allowed
			}

			contentLength := c.Request.ContentLength
			if contentLength > 0 {
				// net/http stops the body at its Content-Length, so charging
				// the declared size up front is exact.
				if !charge(contentLength) {
					return
				}
			} else if contentLength < 0 && c.Request.Body != nil && c.Request.Body != http.NoBody {
				// No declared size (chunked transfer encoding): charge the
				// bytes as the handler reads them and cut the body off once
				// over. The refusal is the response, so whatever the handler
				// writes after its failed read is dropped.
				w := &refusableWriter{ResponseWriter: c.Writer}
				c.Writer = w
				c.Request.Body = &meteredBody{
					ReadCloser: c.Request.Body,
					charge: func(n int64) bool {
						if charge(n) {
							return true
						}
						w.closed = true
						return false
					},
				}
			}
		}

		// Process request
		c.Next()

		// For downloads, track bytes after response (for metrics only - can't block after)
		if c.Request.Method == "GET" {
			bytesWritten := int64(c.Writer.Size())
			if bytesWritten > 0 {
				// Just track, don't block (response already sent)
				_, _, _ = limiter.AllowBytes(
					c.Request.Context(),
					"download:"+key,
					bytesWritten,
					downloadLimitBytes,
					window,
				)
			}
		}
	}
}

// classifyRequest determines the type of request for rate limiting purposes.
func classifyRequest(c *gin.Context) string {
	method := c.Request.Method
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}

	// Upload requests
	if method == "PUT" || method == "POST" {
		if strings.Contains(path, "/upload") || strings.Contains(path, "/media") ||
			strings.Contains(path, "/mirror") || strings.Contains(path, "/transcode") {
			return "upload"
		}
	}
	// Resumable (tus) uploads: the create and every chunk carry upload bytes.
	if (method == "POST" && path == "/files") || (method == "PATCH" && strings.HasPrefix(path, "/files/")) {
		return "upload"
	}

	// Download requests (blob retrieval)
	if method == "GET" {
		// Blob download paths: /:hash, /:hash/thumb, /:hash/hls/*
		if !strings.HasPrefix(path, "/list") &&
			!strings.HasPrefix(path, "/stats") &&
			!strings.HasPrefix(path, "/metrics") &&
			!strings.HasPrefix(path, "/admin") &&
			!strings.HasPrefix(path, "/transparency") &&
			!strings.HasPrefix(path, "/.well-known") {
			return "download"
		}
	}

	return "general"
}

// parseDuration parses a duration string, returning defaultVal on error.
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

// errBandwidthExceeded is what a metered body's Read returns once the address
// is over its upload cap.
var errBandwidthExceeded = errors.New("bandwidth limit exceeded")

// meterSlice is how many bytes a metered body reads between charges, so a
// large upload costs a limiter call per MiB rather than per Read.
const meterSlice = 1 << 20

// meteredBody charges a request body against the upload cap as it is read.
type meteredBody struct {
	io.ReadCloser
	charge  func(n int64) bool
	pending int64
	refused bool
}

func (m *meteredBody) Read(p []byte) (int, error) {
	if m.refused {
		return 0, errBandwidthExceeded
	}
	n, err := m.ReadCloser.Read(p)
	m.pending += int64(n)
	if m.pending >= meterSlice || (err != nil && m.pending > 0) {
		if !m.charge(m.pending) {
			m.refused = true
			// The bytes just read into p are dropped: reporting 0 keeps them
			// from the handler.
			return 0, errBandwidthExceeded
		}
		m.pending = 0
	}
	return n, err
}

// refusableWriter drops everything written after the limiter has answered.
type refusableWriter struct {
	gin.ResponseWriter
	closed bool
}

func (w *refusableWriter) WriteHeader(code int) {
	if !w.closed {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *refusableWriter) WriteHeaderNow() {
	if !w.closed {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *refusableWriter) Write(b []byte) (int, error) {
	if w.closed {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *refusableWriter) WriteString(s string) (int, error) {
	if w.closed {
		return len(s), nil
	}
	return w.ResponseWriter.WriteString(s)
}
