package gin

import (
	"net/http"
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
		if !conf.Enabled {
			c.Next()
			return
		}

		// Determine request type
		requestType := classifyRequest(c)

		// Get identifier (pubkey for authenticated, IP for anonymous)
		pubkey := c.GetString("pk")
		var key string
		var limitConfig config.RateLimitConfig
		var window time.Duration

		if pubkey != "" {
			// Check whitelist
			if whitelist[pubkey] {
				c.Next()
				return
			}

			key = "pk:" + pubkey
			switch requestType {
			case "download":
				limitConfig = conf.Pubkey.Download
				window = pubkeyDownloadWindow
			case "upload":
				limitConfig = conf.Pubkey.Upload
				window = pubkeyUploadWindow
			default:
				limitConfig = conf.Pubkey.General
				window = pubkeyGeneralWindow
			}
		} else {
			// Use client IP
			ip := c.ClientIP()
			key = "ip:" + ip
			switch requestType {
			case "download":
				limitConfig = conf.IP.Download
				window = ipDownloadWindow
			case "upload":
				limitConfig = conf.IP.Upload
				window = ipUploadWindow
			default:
				limitConfig = conf.IP.General
				window = ipGeneralWindow
			}
		}

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

			c.AbortWithStatusJSON(http.StatusTooManyRequests, apiError{
				Message: "rate limit exceeded, please try again later",
			})
			return
		}

		c.Next()
	}
}

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
		if !conf.Enabled {
			c.Next()
			return
		}

		// Check whitelist
		pubkey := c.GetString("pk")
		if pubkey != "" && whitelist[pubkey] {
			c.Next()
			return
		}

		// Determine key
		var key string
		if pubkey != "" {
			key = "pk:" + pubkey
		} else {
			key = "ip:" + c.ClientIP()
		}

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

					c.AbortWithStatusJSON(http.StatusTooManyRequests, apiError{
						Message: "bandwidth limit exceeded, please try again later",
					})
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
