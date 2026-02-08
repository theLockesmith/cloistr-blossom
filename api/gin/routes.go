package gin

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/core"
	"go.uber.org/zap"
)

func SetupRoutes(
	services core.Services,
	cdnBaseUrl string,
	adminPubkey string,
	log *zap.Logger,
) *gin.Engine {
	r := gin.New()

	r.Use(ginzap.Ginzap(log, time.RFC3339, true))
	r.Use(ginzap.RecoveryWithZap(log, true))

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
