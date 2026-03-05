package gin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

type AIModerationController struct {
	aiMod core.AIModerationService
}

func NewAIModerationController(aiMod core.AIModerationService) *AIModerationController {
	return &AIModerationController{aiMod: aiMod}
}

// GetStats returns AI moderation statistics.
// GET /admin/ai-moderation/stats
func (c *AIModerationController) GetStats(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	stats, err := c.aiMod.GetStats(ctx.Request.Context())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, stats)
}

// ListQuarantined returns quarantined blobs pending review.
// GET /admin/ai-moderation/quarantine
func (c *AIModerationController) ListQuarantined(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	status := ctx.Query("status")
	if status == "" {
		status = "pending"
	}

	limit := 50
	if l := ctx.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	offset := 0
	if o := ctx.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	blobs, err := c.aiMod.ListQuarantinedBlobs(ctx.Request.Context(), status, limit, offset)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"blobs":  blobs,
		"count":  len(blobs),
		"limit":  limit,
		"offset": offset,
	})
}

// GetQuarantinedBlob returns a specific quarantined blob.
// GET /admin/ai-moderation/quarantine/:hash
func (c *AIModerationController) GetQuarantinedBlob(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash required"})
		return
	}

	blob, err := c.aiMod.GetQuarantinedBlob(ctx.Request.Context(), hash)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, blob)
}

// ReviewQuarantinedBlob approves or rejects a quarantined blob.
// POST /admin/ai-moderation/quarantine/:hash/review
func (c *AIModerationController) ReviewQuarantinedBlob(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash required"})
		return
	}

	var req struct {
		Approved bool `json:"approved"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Get reviewer pubkey from context (set by auth middleware)
	reviewerPubkey := ctx.GetString("pubkey")
	if reviewerPubkey == "" {
		reviewerPubkey = "admin"
	}

	if err := c.aiMod.ReviewQuarantinedBlob(ctx.Request.Context(), hash, req.Approved, reviewerPubkey); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	action := "rejected"
	if req.Approved {
		action = "approved"
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":  action,
		"hash":    hash,
		"message": "review completed",
	})
}

// GetProviders returns information about registered AI providers.
// GET /admin/ai-moderation/providers
func (c *AIModerationController) GetProviders(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	providers := c.aiMod.GetProviders()
	providerInfo := make([]gin.H, 0, len(providers))

	for _, p := range providers {
		providerInfo = append(providerInfo, gin.H{
			"name":        p.Name(),
			"mime_types":  p.SupportedMimeTypes(),
			"categories":  p.SupportedCategories(),
			"available":   p.IsAvailable(ctx.Request.Context()),
		})
	}

	ctx.JSON(http.StatusOK, gin.H{
		"enabled":   c.aiMod.IsEnabled(),
		"providers": providerInfo,
	})
}

// ScanBlob manually triggers a scan of an existing blob.
// POST /admin/ai-moderation/scan/:hash
func (c *AIModerationController) ScanBlob(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash required"})
		return
	}

	// For manual scans, we queue them asynchronously
	// In a real implementation, you'd fetch the blob data and scan it
	req := &core.ScanRequest{
		Hash:     hash,
		MimeType: ctx.Query("mime_type"),
	}

	queueID, err := c.aiMod.ScanContentAsync(ctx.Request.Context(), req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusAccepted, gin.H{
		"message":  "scan queued",
		"hash":     hash,
		"queue_id": queueID,
	})
}

// GetScanResult returns the cached scan result for a blob.
// GET /admin/ai-moderation/scan/:hash
func (c *AIModerationController) GetScanResult(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash required"})
		return
	}

	result, err := c.aiMod.GetScanResult(ctx.Request.Context(), hash)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "no scan result found"})
		return
	}

	ctx.JSON(http.StatusOK, result)
}

// GetQueueStatus returns the current scan queue status.
// GET /admin/ai-moderation/queue
func (c *AIModerationController) GetQueueStatus(ctx *gin.Context) {
	if c.aiMod == nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "AI moderation not enabled"})
		return
	}

	size, err := c.aiMod.GetQueueSize(ctx.Request.Context())
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"queue_size": size,
	})
}

// RegisterAIModerationRoutes registers AI moderation admin routes.
func RegisterAIModerationRoutes(r *gin.Engine, controller *AIModerationController, adminMiddleware gin.HandlerFunc) {
	if controller == nil || controller.aiMod == nil {
		return
	}

	admin := r.Group("/admin/ai-moderation")
	admin.Use(adminMiddleware)
	{
		admin.GET("/stats", controller.GetStats)
		admin.GET("/providers", controller.GetProviders)
		admin.GET("/queue", controller.GetQueueStatus)

		// Quarantine management
		admin.GET("/quarantine", controller.ListQuarantined)
		admin.GET("/quarantine/:hash", controller.GetQuarantinedBlob)
		admin.POST("/quarantine/:hash/review", controller.ReviewQuarantinedBlob)

		// Manual scanning
		admin.POST("/scan/:hash", controller.ScanBlob)
		admin.GET("/scan/:hash", controller.GetScanResult)
	}
}
