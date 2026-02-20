package gin

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/ratelimit"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"go.uber.org/zap"
)

func SetupRoutes(
	services core.Services,
	cdnBaseUrl string,
	adminPubkey string,
	conf *config.Config,
	appCache cache.Cache,
	log *zap.Logger,
) *gin.Engine {
	r := gin.New()

	r.Use(ginzap.Ginzap(log, time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(log, true))
	r.Use(MetricsMiddleware())

	// Rate limiting middleware
	if conf.RateLimiting.Enabled {
		rateLimiter := ratelimit.NewRateLimiter(appCache)
		bandwidthLimiter := ratelimit.NewBandwidthLimiter(appCache)
		r.Use(RateLimitMiddleware(rateLimiter, &conf.RateLimiting, log))
		r.Use(BandwidthLimitMiddleware(bandwidthLimiter, &conf.RateLimiting, log))
		log.Info("rate limiting enabled")
	}

	r.Use(cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "PUT", "POST", "HEAD", "DELETE"},
		AllowHeaders: []string{
			HeaderAuthorization,
			HeaderContentType,
			HeaderXSHA256,
			HeaderXContentType,
			HeaderXContentLength,
		},
		ExposeHeaders: []string{"Content-Length"},
	}))

	r.GET("/.well-known/health", func(ctx *gin.Context) {
		ctx.Status(http.StatusOK)
	})

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.HEAD(
		"/upload",
		nostrAuthMiddleware("upload", log),
		uploadRequirements(services),
	)
	r.PUT(
		"/upload",
		nostrAuthMiddleware("upload", log),
		uploadBlob(
			services,
			cdnBaseUrl,
		),
	)

	r.PUT(
		"/mirror",
		nostrAuthMiddleware("upload", log),
		mirrorBlob(
			services,
			cdnBaseUrl,
		),
	)

	// BUD-05: Media optimization endpoint
	r.HEAD(
		"/media",
		nostrAuthMiddleware("media", log),
		mediaRequirements(services),
	)
	r.PUT(
		"/media",
		nostrAuthMiddleware("media", log),
		uploadMedia(services, cdnBaseUrl),
	)

	// Thumbnail generation endpoint
	r.GET(
		"/:hash/thumb",
		getThumbnail(services),
	)

	// Video transcoding and HLS streaming endpoints
	r.POST(
		"/:hash/transcode",
		nostrAuthMiddleware("upload", log),
		startTranscode(services),
	)
	r.GET(
		"/:hash/transcode",
		getTranscodeStatus(services),
	)
	r.GET(
		"/:hash/hls/master.m3u8",
		getHLSMasterPlaylist(services, cdnBaseUrl),
	)
	r.GET(
		"/:hash/hls/:quality/stream.m3u8",
		getHLSVariantPlaylist(services, cdnBaseUrl),
	)
	r.GET(
		"/:hash/hls/:quality/:segment",
		getHLSSegment(services),
	)

	// DASH streaming endpoints
	r.GET(
		"/:hash/dash/manifest.mpd",
		getDASHManifest(services, cdnBaseUrl),
	)
	r.GET(
		"/:hash/dash/:segment",
		getDASHSegment(services),
	)

	// IPFS pinning endpoints
	r.POST(
		"/:hash/pin",
		nostrAuthMiddleware("upload", log),
		pinBlob(services),
	)
	r.DELETE(
		"/:hash/pin",
		nostrAuthMiddleware("delete", log),
		unpinBlob(services),
	)
	r.GET(
		"/:hash/pin",
		getPinStatus(services),
	)
	r.GET(
		"/pins",
		listPins(services),
	)

	// Subtitle endpoints
	r.PUT(
		"/:hash/subtitles/:lang",
		nostrAuthMiddleware("upload", log),
		addSubtitle(services),
	)
	r.GET(
		"/:hash/subtitles/:lang",
		getSubtitle(services),
	)
	r.GET(
		"/:hash/subtitles",
		listSubtitles(services),
	)
	r.DELETE(
		"/:hash/subtitles/:lang",
		nostrAuthMiddleware("delete", log),
		deleteSubtitle(services),
	)

	r.GET(
		"/list/:pubkey",
		listBlobs(services),
	)

	r.GET(
		"/:path",
		getBlob(services),
	)
	r.HEAD(
		"/:path",
		hasBlob(services),
	)

	r.DELETE(
		"/:path",
		nostrAuthMiddleware("delete", log),
		deleteBlob(services),
	)

	// server stats
	r.GET("/stats", getStats(services))

	// Content reporting and transparency
	r.POST("/report", submitReport(services))
	r.GET("/transparency", getTransparencyPage(services))

	// Admin dashboard and API
	RegisterAdminRoutes(r, services, adminPubkey, log)

	return r
}
