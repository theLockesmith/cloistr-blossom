package gin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/metrics"
)

// MetricsMiddleware records request metrics for Prometheus.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		// Process request
		c.Next()

		// Record metrics
		status := strconv.Itoa(c.Writer.Status())
		duration := time.Since(start).Seconds()

		metrics.RequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		metrics.RequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)

		// Record errors
		if c.Writer.Status() >= 400 {
			errorType := "client_error"
			if c.Writer.Status() >= 500 {
				errorType = "server_error"
			}
			metrics.ErrorsTotal.WithLabelValues(errorType).Inc()
		}
	}
}
