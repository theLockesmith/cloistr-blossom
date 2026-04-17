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
			"X-Cashu",           // BUD-07: Cashu payment proof
			"X-Lightning",       // BUD-07: Lightning payment proof
			"X-Payment-Request", // BUD-07: Payment request ID
		},
		ExposeHeaders: []string{
			"Content-Length",
			"X-Cashu",           // BUD-07: Cashu payment request
			"X-Lightning",       // BUD-07: Lightning invoice
			"X-Payment-Request", // BUD-07: Payment request ID
			"X-Payment-Amount",  // BUD-07: Payment amount in sats
			"X-Reason",          // BUD-07: Error reason
		},
	}))

	r.GET("/.well-known/health", func(ctx *gin.Context) {
		ctx.Status(http.StatusOK)
	})

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Payment middleware for upload operations (BUD-07)
	var paymentMW gin.HandlerFunc
	if services.Payment() != nil && services.Payment().IsEnabled() {
		paymentMW = PaymentMiddleware(services.Payment(), log)
		log.Info("payment middleware enabled for uploads")
	}

	r.HEAD(
		"/upload",
		nostrAuthMiddleware("upload", log),
		uploadRequirements(services),
	)

	// Build upload handlers chain with optional payment middleware
	uploadHandlers := []gin.HandlerFunc{nostrAuthMiddleware("upload", log)}
	if paymentMW != nil {
		uploadHandlers = append(uploadHandlers, paymentMW)
	}
	uploadHandlers = append(uploadHandlers, uploadBlob(services, cdnBaseUrl))
	r.PUT("/upload", uploadHandlers...)

	// Mirror endpoint with optional payment
	mirrorHandlers := []gin.HandlerFunc{nostrAuthMiddleware("upload", log)}
	if paymentMW != nil {
		mirrorHandlers = append(mirrorHandlers, paymentMW)
	}
	mirrorHandlers = append(mirrorHandlers, mirrorBlob(services, cdnBaseUrl))
	r.PUT("/mirror", mirrorHandlers...)

	// BUD-05: Media optimization endpoint
	r.HEAD(
		"/media",
		nostrAuthMiddleware("media", log),
		mediaRequirements(services),
	)

	// Media upload with optional payment
	mediaHandlers := []gin.HandlerFunc{nostrAuthMiddleware("media", log)}
	if paymentMW != nil {
		mediaHandlers = append(mediaHandlers, paymentMW)
	}
	mediaHandlers = append(mediaHandlers, uploadMedia(services, cdnBaseUrl))
	r.PUT("/media", mediaHandlers...)

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

	// Torrent endpoints
	r.POST(
		"/:hash/torrent",
		nostrAuthMiddleware("upload", log),
		generateTorrent(services, cdnBaseUrl),
	)
	r.GET(
		"/:hash/torrent",
		getTorrent(services),
	)
	r.DELETE(
		"/:hash/torrent",
		nostrAuthMiddleware("delete", log),
		deleteTorrent(services),
	)

	r.GET(
		"/list/:pubkey",
		listBlobs(services),
	)

	r.GET(
		"/:hash",
		getBlob(services),
	)
	r.HEAD(
		"/:hash",
		hasBlob(services),
	)

	r.DELETE(
		"/:hash",
		nostrAuthMiddleware("delete", log),
		deleteBlob(services),
	)

	// BUD-03: User server list
	r.GET("/servers/:pubkey", getUserServerList(services))

	// server stats
	r.GET("/stats", getStats(services))

	// Content reporting and transparency
	r.POST("/report", submitReport(services))          // Legacy JSON report
	r.PUT("/report", submitReportBUD09(services, log)) // BUD-09 NIP-56 signed report
	r.GET("/transparency", getTransparencyPage(services))

	// Admin dashboard and API
	RegisterAdminRoutes(r, services, adminPubkey, log)

	// Chunked upload endpoints
	if services.ChunkedUpload() != nil {
		chunkedHandler := NewChunkedUploadHandler(services.ChunkedUpload(), cdnBaseUrl)
		RegisterChunkedUploadRoutes(r, chunkedHandler, nostrAuthMiddleware("upload", log))
		log.Info("chunked upload routes registered")
	}

	// TUS resumable upload protocol endpoints
	tusHandler, err := NewTusHandler(
		services.Blob(),
		services.Quota(),
		TusConfig{
			TempDir:    conf.ChunkedUpload.TempDir,
			CDNBaseURL: cdnBaseUrl,
		},
		log,
	)
	if err != nil {
		log.Error("failed to initialize tus handler", zap.Error(err))
	} else {
		RegisterTusRoutes(r, tusHandler, nostrAuthMiddleware("upload", log), log)
	}

	// WebSocket real-time notifications
	if services.Notifications() != nil {
		wsHandler := NewWebSocketHandler(services.Notifications(), log)
		RegisterWebSocketRoutes(r, wsHandler, nostrAuthMiddleware("upload", log))
		log.Info("websocket notification routes registered")
	}

	// Batch operations endpoints
	if services.Batch() != nil {
		batchController := NewBatchController(services.Batch())
		batch := r.Group("/batch")
		{
			// Upload requires auth
			batch.POST("/upload", nostrAuthMiddleware("upload", log), batchController.BatchUpload)

			// Download can be done without auth (public blobs) or with auth
			batch.POST("/download", batchController.BatchDownload)

			// Delete requires auth
			batch.DELETE("", nostrAuthMiddleware("delete", log), batchController.BatchDelete)

			// Status check doesn't require auth
			batch.POST("/status", batchController.BatchStatus)

			// Job management
			batch.GET("/jobs/:job_id", batchController.GetBatchJob)
			batch.DELETE("/jobs/:job_id", nostrAuthMiddleware("delete", log), batchController.CancelBatchJob)
		}
		log.Info("batch operation routes registered")
	}

	// AI moderation admin routes
	if services.AIModeration() != nil {
		aiModController := NewAIModerationController(services.AIModeration())
		RegisterAIModerationRoutes(r, aiModController, adminAuthMiddleware(adminPubkey))
		log.Info("AI moderation routes registered")
	}

	// Federation admin routes
	if services.Federation() != nil && services.Federation().IsEnabled() {
		admin := r.Group("/admin")
		admin.Use(adminAuthMiddleware(adminPubkey))
		RegisterFederationRoutes(admin, services)
		log.Info("federation routes registered")
	}

	return r
}
