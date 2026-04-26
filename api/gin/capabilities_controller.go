package gin

import (
	"net/http"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"github.com/gin-gonic/gin"
)

// ServerCapabilities represents the server's advertised capabilities.
// This is returned at /.well-known/blossom for client discovery.
type ServerCapabilities struct {
	// Server identification
	Name    string `json:"name"`
	Version string `json:"version"`
	Pubkey  string `json:"pubkey,omitempty"` // Server's Nostr pubkey if configured

	// Supported BUDs (Blossom Upgrade Documents)
	BUDs []string `json:"buds"`

	// Feature capabilities
	Features FeatureCapabilities `json:"features"`

	// Limits
	Limits LimitCapabilities `json:"limits"`

	// Payment info (only present if payments enabled)
	Payment *PaymentCapabilities `json:"payment,omitempty"`

	// Contact and policy
	Contact     string `json:"contact,omitempty"`
	TOS         string `json:"tos,omitempty"`
	ContentNote string `json:"content_note,omitempty"`
}

// FeatureCapabilities describes optional server features.
type FeatureCapabilities struct {
	// Storage features
	Encryption bool `json:"encryption"` // Server-side encryption at rest
	CDN        bool `json:"cdn"`        // CDN delivery available

	// Media features
	MediaOptimization bool     `json:"media_optimization"` // BUD-05 media processing
	Transcoding       bool     `json:"transcoding"`        // HLS/DASH video transcoding
	Thumbnails        bool     `json:"thumbnails"`         // Thumbnail generation
	Subtitles         bool     `json:"subtitles"`          // Subtitle support
	SupportedFormats  []string `json:"supported_formats,omitempty"`

	// Protocol features
	ChunkedUpload    bool `json:"chunked_upload"`    // Chunked/resumable uploads
	TusUpload        bool `json:"tus_upload"`        // TUS protocol support
	BatchOperations  bool `json:"batch_operations"`  // Batch upload/download
	WebSocketNotify  bool `json:"websocket_notify"`  // Real-time notifications
	Federation       bool `json:"federation"`        // Server federation
	ContentModeration bool `json:"content_moderation"` // AI content moderation

	// Distribution features
	IPFS    bool `json:"ipfs"`    // IPFS pinning
	Torrent bool `json:"torrent"` // Torrent/BEP-19 support
}

// LimitCapabilities describes server limits.
type LimitCapabilities struct {
	MaxUploadSize    int64 `json:"max_upload_size"`              // Max single upload in bytes
	DefaultQuota     int64 `json:"default_quota,omitempty"`      // Default storage quota
	MaxQuota         int64 `json:"max_quota,omitempty"`          // Maximum storage quota
	RateLimitEnabled bool  `json:"rate_limit_enabled"`           // Rate limiting active
}

// PaymentCapabilities describes payment options.
type PaymentCapabilities struct {
	Required       bool    `json:"required"`                   // Payments required for uploads
	FreeTierBytes  int64   `json:"free_tier_bytes,omitempty"`  // Free tier allowance
	SatoshisPerByte float64 `json:"satoshis_per_byte"`          // Pricing
	MinPaymentSats int64   `json:"min_payment_sats"`           // Minimum payment
	Methods        []string `json:"methods"`                    // Accepted payment methods
}

// getServerCapabilities returns the server capabilities endpoint handler.
func getServerCapabilities(services core.Services, conf *config.Config, adminPubkey string) gin.HandlerFunc {
	// Pre-compute capabilities at startup (they don't change at runtime)
	caps := buildCapabilities(services, conf, adminPubkey)

	return func(c *gin.Context) {
		c.JSON(http.StatusOK, caps)
	}
}

// buildCapabilities constructs the capabilities response from config and services.
func buildCapabilities(services core.Services, conf *config.Config, adminPubkey string) *ServerCapabilities {
	caps := &ServerCapabilities{
		Name:    "Cloistr Blossom",
		Version: "1.2.0",
		Pubkey:  adminPubkey,
	}

	// Supported BUDs
	caps.BUDs = []string{
		"BUD-01", // Server requirements
		"BUD-02", // Blob retrieval
		"BUD-03", // User server lists
		"BUD-04", // Mirroring
		"BUD-05", // Media optimization
		"BUD-06", // Authorization
		"BUD-08", // Blob management
		"BUD-09", // Reporting
		"BUD-10", // URI schema
		"BUD-11", // Delete events
	}

	// Add BUD-07 if payments enabled
	if conf.Payment.Enabled {
		caps.BUDs = append(caps.BUDs, "BUD-07")
	}

	// Feature capabilities
	caps.Features = FeatureCapabilities{
		Encryption:        conf.Encryption.Enabled,
		CDN:               conf.CDN.Enabled,
		MediaOptimization: true, // Always available
		Transcoding:       conf.Transcoding.WorkDir != "",
		Thumbnails:        true,
		Subtitles:         true,
		SupportedFormats:  conf.AllowedMimeTypes,
		ChunkedUpload:     conf.ChunkedUpload.Enabled,
		TusUpload:         true, // TUS always available
		BatchOperations:   services != nil && services.Batch() != nil,
		WebSocketNotify:   services != nil && services.Notifications() != nil,
		Federation:        services != nil && services.Federation() != nil && services.Federation().IsEnabled(),
		ContentModeration: services != nil && services.AIModeration() != nil,
		IPFS:              conf.IPFS.Enabled,
		Torrent:           true, // Torrent generation always available
	}

	// Limits
	caps.Limits = LimitCapabilities{
		MaxUploadSize:    int64(conf.MaxUploadSizeBytes),
		DefaultQuota:     conf.Quota.DefaultBytes,
		MaxQuota:         conf.Quota.MaxBytes,
		RateLimitEnabled: conf.RateLimiting.Enabled,
	}

	// Payment capabilities
	if conf.Payment.Enabled {
		methods := []string{}
		if conf.Payment.Lightning.Enabled {
			methods = append(methods, "lightning")
		}
		if conf.Payment.Cashu.Enabled {
			methods = append(methods, "cashu")
		}

		caps.Payment = &PaymentCapabilities{
			Required:        true,
			FreeTierBytes:   conf.Payment.FreeBytesLimit,
			SatoshisPerByte: conf.Payment.SatoshisPerByte,
			MinPaymentSats:  conf.Payment.MinPaymentSats,
			Methods:         methods,
		}

		// If free tier covers everything, payments aren't really required
		if conf.Payment.FreeBytesLimit > 0 {
			caps.Payment.Required = false
		}
	}

	return caps
}
