package gin

import (
	clerrors "git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"strconv"
	"strings"
	"time"

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
		if !checkRateLimit(c, limiter, log, "ip:"+ip, requestType, ipLimit, ipWindow) {
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
		if pubkey != "" && !checkRateLimit(c, limiter, log, "pk:"+pubkey, requestType, pkLimit, pkWindow) {
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

		// For uploads, check bandwidth before processing
		if c.Request.Method == "PUT" || c.Request.Method == "POST" {
			contentLength := c.Request.ContentLength
			if contentLength > 0 {
				allowed, remaining, resetAt := limiter.AllowBytes(
					c.Request.Context(),
					"upload:"+key,
					contentLength,
					uploadLimitBytes,
					window,
				)

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
						zap.Int64("bytes", contentLength))

					metrics.RateLimitedTotal.WithLabelValues("bandwidth_upload").Inc()

					clerrors.TooManyRequests(clerrors.CodeRateLimitExceeded, "bandwidth limit exceeded, please try again later", retryAfter).Abort(c)
					return
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
