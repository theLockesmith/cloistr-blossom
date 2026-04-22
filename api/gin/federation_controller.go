package gin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// FederationStatsResponse represents federation statistics.
type FederationStatsResponse struct {
	ServerPubkey    string `json:"server_pubkey"`
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	RelayCount      int    `json:"relay_count"`
	EventsPublished int64  `json:"events_published"`
	EventsPending   int64  `json:"events_pending"`
	EventsFailed    int64  `json:"events_failed"`
	EventsReceived  int64  `json:"events_received"`
	BlobsDiscovered int64  `json:"blobs_discovered"`
	BlobsMirrored   int64  `json:"blobs_mirrored"`
	MirrorsPending  int64  `json:"mirrors_pending"`
	KnownServers    int64  `json:"known_servers"`
	HealthyServers  int64  `json:"healthy_servers"`
	FederatedUsers  int64  `json:"federated_users"`
	LastPublished   int64  `json:"last_published,omitempty"`
	LastReceived    int64  `json:"last_received,omitempty"`
	LastMirror      int64  `json:"last_mirror,omitempty"`
	WorkersActive   int    `json:"workers_active"`
	QueueSize       int    `json:"queue_size"`
}

// FederatedBlobResponse represents a federated blob.
type FederatedBlobResponse struct {
	Hash         string   `json:"hash"`
	Size         int64    `json:"size"`
	MimeType     string   `json:"mime_type"`
	URLs         []string `json:"urls,omitempty"`
	RefCount     int      `json:"ref_count"`
	Status       string   `json:"status"`
	DiscoveredAt int64    `json:"discovered_at"`
	MirroredAt   int64    `json:"mirrored_at,omitempty"`
	LastSeenAt   int64    `json:"last_seen_at"`
}

// KnownServerResponse represents a known Blossom server.
type KnownServerResponse struct {
	URL       string `json:"url"`
	Pubkey    string `json:"pubkey,omitempty"`
	UserCount int    `json:"user_count"`
	BlobCount int    `json:"blob_count"`
	Healthy   bool   `json:"healthy"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	LastCheck int64  `json:"last_check,omitempty"`
}

// FederationEventResponse represents a federation event.
type FederationEventResponse struct {
	ID          string `json:"id"`
	EventID     string `json:"event_id,omitempty"`
	EventKind   int    `json:"event_kind"`
	Pubkey      string `json:"pubkey"`
	BlobHash    string `json:"blob_hash,omitempty"`
	Direction   string `json:"direction"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	RelayURL    string `json:"relay_url,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	PublishedAt int64  `json:"published_at,omitempty"`
	Retries     int    `json:"retries"`
}

// getFederationStatus returns federation configuration and status.
func getFederationStatus(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil {
			ctx.JSON(http.StatusOK, gin.H{
				"enabled": false,
				"message": "federation service not initialized",
			})
			return
		}

		stats, err := federation.Stats(ctx.Request.Context())
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, FederationStatsResponse{
			ServerPubkey:    stats.ServerPubkey,
			Enabled:         stats.Enabled,
			Mode:            string(stats.Mode),
			RelayCount:      stats.RelayCount,
			EventsPublished: stats.EventsPublished,
			EventsPending:   stats.EventsPending,
			EventsFailed:    stats.EventsFailed,
			EventsReceived:  stats.EventsReceived,
			BlobsDiscovered: stats.BlobsDiscovered,
			BlobsMirrored:   stats.BlobsMirrored,
			MirrorsPending:  stats.MirrorsPending,
			KnownServers:    stats.KnownServers,
			HealthyServers:  stats.HealthyServers,
			FederatedUsers:  stats.FederatedUsers,
			LastPublished:   stats.LastPublished,
			LastReceived:    stats.LastReceived,
			LastMirror:      stats.LastMirror,
			WorkersActive:   stats.WorkersActive,
			QueueSize:       stats.QueueSize,
		})
	}
}

// listFederatedBlobs returns a paginated list of federated blobs.
func listFederatedBlobs(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.JSON(http.StatusOK, gin.H{"blobs": []interface{}{}, "message": "federation disabled"})
			return
		}

		limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
		status := ctx.DefaultQuery("status", "discovered")

		if limit > 100 {
			limit = 100
		}

		blobs, err := federation.ListFederatedBlobs(ctx.Request.Context(), core.FederatedBlobStatus(status), limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		response := make([]FederatedBlobResponse, len(blobs))
		for i, b := range blobs {
			response[i] = FederatedBlobResponse{
				Hash:         b.Hash,
				Size:         b.Size,
				MimeType:     b.MimeType,
				URLs:         b.URLs,
				RefCount:     b.RefCount,
				Status:       string(b.Status),
				DiscoveredAt: b.DiscoveredAt,
				MirroredAt:   b.MirroredAt,
				LastSeenAt:   b.LastSeenAt,
			}
		}

		ctx.JSON(http.StatusOK, gin.H{"blobs": response})
	}
}

// getFederatedBlob returns details for a specific federated blob.
func getFederatedBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "federation disabled"})
			return
		}

		hash := ctx.Param("hash")
		blob, err := federation.GetFederatedBlob(ctx.Request.Context(), hash)
		if err != nil {
			if err == core.ErrFederatedBlobNotFound {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "federated blob not found"})
				return
			}
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, FederatedBlobResponse{
			Hash:         blob.Hash,
			Size:         blob.Size,
			MimeType:     blob.MimeType,
			URLs:         blob.URLs,
			RefCount:     blob.RefCount,
			Status:       string(blob.Status),
			DiscoveredAt: blob.DiscoveredAt,
			MirroredAt:   blob.MirroredAt,
			LastSeenAt:   blob.LastSeenAt,
		})
	}
}

// mirrorFederatedBlob triggers mirroring of a federated blob.
func mirrorFederatedBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "federation disabled"})
			return
		}

		hash := ctx.Param("hash")
		if err := federation.MirrorBlobAsync(ctx.Request.Context(), hash); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusAccepted, gin.H{"message": "mirror job queued", "hash": hash})
	}
}

// publishBlobToFederation triggers republishing of a blob to federation.
func publishBlobToFederation(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "federation disabled"})
			return
		}

		hash := ctx.Param("hash")
		if err := federation.RepublishBlob(ctx.Request.Context(), hash); err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusAccepted, gin.H{"message": "republish job queued", "hash": hash})
	}
}

// listKnownServers returns a paginated list of known servers.
func listKnownServers(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.JSON(http.StatusOK, gin.H{"servers": []interface{}{}, "message": "federation disabled"})
			return
		}

		limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))

		if limit > 100 {
			limit = 100
		}

		servers, err := federation.GetKnownServers(ctx.Request.Context(), limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		response := make([]KnownServerResponse, len(servers))
		for i, s := range servers {
			response[i] = KnownServerResponse{
				URL:       s.URL,
				Pubkey:    s.Pubkey,
				UserCount: s.UserCount,
				BlobCount: s.BlobCount,
				Healthy:   s.Healthy,
				FirstSeen: s.FirstSeen,
				LastSeen:  s.LastSeen,
				LastCheck: s.LastCheck,
			}
		}

		ctx.JSON(http.StatusOK, gin.H{"servers": response})
	}
}

// checkServerHealth triggers a health check for a server.
func checkServerHealth(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "federation disabled"})
			return
		}

		serverURL := ctx.Query("url")
		if serverURL == "" {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: "url parameter required"})
			return
		}

		healthy, err := federation.CheckServerHealth(ctx.Request.Context(), serverURL)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"url": serverURL, "healthy": healthy})
	}
}

// listFederationEvents returns a paginated list of federation events.
func listFederationEvents(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.JSON(http.StatusOK, gin.H{"events": []interface{}{}, "message": "federation disabled"})
			return
		}

		limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
		direction := ctx.Query("direction") // "publish" or "receive", empty for all

		if limit > 100 {
			limit = 100
		}

		events, err := federation.GetEventHistory(ctx.Request.Context(), direction, limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		response := make([]FederationEventResponse, len(events))
		for i, e := range events {
			response[i] = FederationEventResponse{
				ID:          e.ID,
				EventID:     e.EventID,
				EventKind:   e.EventKind,
				Pubkey:      e.Pubkey,
				BlobHash:    e.BlobHash,
				Direction:   e.Direction,
				Status:      string(e.Status),
				Error:       e.Error,
				RelayURL:    e.RelayURL,
				CreatedAt:   e.CreatedAt,
				PublishedAt: e.PublishedAt,
				Retries:     e.Retries,
			}
		}

		ctx.JSON(http.StatusOK, gin.H{"events": response})
	}
}

// listFederatedUsers returns users who have this server in their server list.
func listFederatedUsers(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		federation := services.Federation()
		if federation == nil || !federation.IsEnabled() {
			ctx.JSON(http.StatusOK, gin.H{"users": []interface{}{}, "message": "federation disabled"})
			return
		}

		limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(ctx.DefaultQuery("offset", "0"))

		if limit > 100 {
			limit = 100
		}

		users, err := federation.GetFederatedUsers(ctx.Request.Context(), limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{"users": users})
	}
}

// RegisterFederationRoutes registers federation admin routes.
func RegisterFederationRoutes(r *gin.RouterGroup, services core.Services) {
	federation := r.Group("/federation")

	federation.GET("/status", getFederationStatus(services))
	federation.GET("/stats", getFederationStatus(services)) // Alias

	federation.GET("/blobs", listFederatedBlobs(services))
	federation.GET("/blobs/:hash", getFederatedBlob(services))
	federation.POST("/blobs/:hash/mirror", mirrorFederatedBlob(services))
	federation.POST("/blobs/:hash/publish", publishBlobToFederation(services))

	federation.GET("/servers", listKnownServers(services))
	federation.POST("/servers/health-check", checkServerHealth(services))

	federation.GET("/events", listFederationEvents(services))
	federation.GET("/users", listFederatedUsers(services))
}
