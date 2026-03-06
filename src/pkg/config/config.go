package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type AccessControlRule struct {
	Action   string `yaml:"action"`
	Pubkey   string `yaml:"pubkey"`
	Resource string `yaml:"resource"`
}

// PlatformConfig defines Cloistr unified platform integration settings.
type PlatformConfig struct {
	// Mode can be "platform" (use shared PostgreSQL) or "standalone" (use local config)
	// If empty, defaults to "standalone" for backwards compatibility
	Mode string `yaml:"mode"`

	// DatabaseURL is the connection string for the unified platform database
	// Required when Mode is "platform"
	// Example: postgres://cloistr:password@postgres-rw.db.coldforge.xyz:5432/cloistr
	DatabaseURL string `yaml:"database_url"`

	// ServiceID identifies this service in the platform (default: "blossom")
	ServiceID string `yaml:"service_id"`
}

// StorageConfig defines the blob storage backend configuration.
type StorageConfig struct {
	Backend string             `yaml:"backend"` // "local" or "s3"
	Local   LocalStorageConfig `yaml:"local"`
	S3      S3StorageConfig    `yaml:"s3"`
}

// LocalStorageConfig defines local filesystem storage settings.
type LocalStorageConfig struct {
	Path string `yaml:"path"` // Directory to store blobs
}

// S3StorageConfig defines S3-compatible storage settings.
type S3StorageConfig struct {
	Endpoint  string `yaml:"endpoint"`   // S3-compatible endpoint URL
	Bucket    string `yaml:"bucket"`     // Bucket name
	Region    string `yaml:"region"`     // AWS region
	AccessKey string `yaml:"access_key"` // Access key ID (can use env var)
	SecretKey string `yaml:"secret_key"` // Secret access key (can use env var)
	PathStyle bool   `yaml:"path_style"` // Use path-style addressing (for MinIO/Ceph)
}

// DatabaseConfig defines database connection settings.
type DatabaseConfig struct {
	Driver   string         `yaml:"driver"` // "sqlite" or "postgres"
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	Postgres PostgresConfig `yaml:"postgres"`
}

// SQLiteConfig defines SQLite-specific settings.
type SQLiteConfig struct {
	Path string `yaml:"path"` // Path to SQLite database file
}

// PostgresConfig defines PostgreSQL-specific settings.
type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"`
}

// QuotaConfig defines storage quota settings.
type QuotaConfig struct {
	Enabled       bool  `yaml:"enabled"`
	DefaultBytes  int64 `yaml:"default_bytes"`  // Default quota for new users
	MaxBytes      int64 `yaml:"max_bytes"`      // Maximum allowed quota
	WarnThreshold int   `yaml:"warn_threshold"` // Percentage at which to warn (e.g., 80)
}

// CacheConfig defines optional cache settings (Redis/Dragonfly).
type CacheConfig struct {
	URL string `yaml:"url"` // Redis URL (e.g. redis://host:6379 or redis://:password@host:6379)
	TTL int    `yaml:"ttl"` // Default TTL in seconds (0 = no expiration)
}

// EncryptionConfig defines encryption settings for stored blobs.
type EncryptionConfig struct {
	Enabled   bool   `yaml:"enabled"`    // Enable server-side encryption at rest
	MasterKey string `yaml:"master_key"` // 32-byte hex-encoded master key (KEK)
	// If MasterKey is empty, one will be auto-generated on first run
	// WARNING: Losing the master key means losing access to all encrypted data
}

// CDNConfig defines CDN integration settings.
type CDNConfig struct {
	Enabled bool `yaml:"enabled"` // Enable CDN delivery

	// Public URL for direct access (e.g., https://cdn.example.com)
	// Used when the bucket/storage is publicly accessible
	PublicURL string `yaml:"public_url"`

	// Use presigned URLs for private bucket access
	// If true, generates time-limited presigned URLs for downloads
	PresignedURLs bool `yaml:"presigned_urls"`

	// Presigned URL expiry duration (e.g., "1h", "24h")
	// Default: 1h
	PresignedExpiry string `yaml:"presigned_expiry"`

	// Redirect to CDN instead of proxying content
	// If true, returns 302 redirects to CDN URLs
	// If false, proxies content through the API
	Redirect bool `yaml:"redirect"`

	// CacheControl header value for CDN-served content
	// Default: "public, max-age=31536000" (1 year for immutable content)
	CacheControl string `yaml:"cache_control"`
}

// IPFSConfig defines IPFS pinning service configuration.
type IPFSConfig struct {
	Enabled bool `yaml:"enabled"` // Enable IPFS pinning

	// Pinning service endpoint URL (IPFS Pinning Service API compatible)
	// Examples:
	//   - Pinata: https://api.pinata.cloud/psa
	//   - web3.storage: https://api.web3.storage
	//   - Filebase: https://api.filebase.io/v1/ipfs
	Endpoint string `yaml:"endpoint"`

	// Bearer token for authentication
	BearerToken string `yaml:"bearer_token"`

	// Gateway URL for accessing pinned content (optional)
	// Examples:
	//   - Public: https://ipfs.io/ipfs/
	//   - Cloudflare: https://cloudflare-ipfs.com/ipfs/
	//   - Pinata: https://gateway.pinata.cloud/ipfs/
	GatewayURL string `yaml:"gateway_url"`

	// Auto-pin newly uploaded blobs
	AutoPin bool `yaml:"auto_pin"`
}

// ChunkedUploadConfig defines chunked upload settings.
type ChunkedUploadConfig struct {
	Enabled           bool   `yaml:"enabled"`             // Enable chunked uploads
	DefaultChunkSize  int64  `yaml:"default_chunk_size"`  // Default chunk size in bytes (default: 5MB)
	MinChunkSize      int64  `yaml:"min_chunk_size"`      // Minimum chunk size (default: 1MB)
	MaxChunkSize      int64  `yaml:"max_chunk_size"`      // Maximum chunk size (default: 100MB)
	MaxSessionTTL     string `yaml:"max_session_ttl"`     // Maximum session lifetime (default: 24h)
	DefaultSessionTTL string `yaml:"default_session_ttl"` // Default session lifetime (default: 1h)
	TempDir           string `yaml:"temp_dir"`            // Directory for temporary chunks
}

// TranscodingConfig defines video transcoding settings.
type TranscodingConfig struct {
	// Work directory for temporary transcoding files
	WorkDir string `yaml:"work_dir"`

	// Path to FFmpeg binary (empty = auto-detect)
	FFmpegPath string `yaml:"ffmpeg_path"`

	// Hardware acceleration settings
	HWAccel HWAccelConfig `yaml:"hwaccel"`
}

// HWAccelConfig defines hardware acceleration settings for video transcoding.
type HWAccelConfig struct {
	// Type of hardware acceleration to use
	// Options: "none", "nvenc", "qsv", "vaapi", "auto"
	// Default: "none" (software encoding)
	Type string `yaml:"type"`

	// Device path for VAAPI (e.g., /dev/dri/renderD128)
	// Only used when type is "vaapi"
	Device string `yaml:"device"`

	// Encoder preset (varies by encoder)
	// - NVENC: p1-p7 (p1=fastest, p7=highest quality), default: p4
	// - QSV: veryfast, faster, fast, medium, slow, slower, veryslow
	// - libx264: ultrafast, superfast, veryfast, faster, fast, medium, slow, slower, veryslow
	Preset string `yaml:"preset"`

	// Look-ahead frames for NVENC (0 = disabled)
	// Higher values improve quality but increase latency and memory usage
	LookAhead int `yaml:"look_ahead"`
}

// RateLimitConfig defines a single rate limit.
type RateLimitConfig struct {
	Requests int    `yaml:"requests"` // Max requests per window
	Window   string `yaml:"window"`   // Time window (e.g., "1m", "1h")
}

// RateLimitingConfig defines rate limiting settings.
type RateLimitingConfig struct {
	Enabled bool `yaml:"enabled"` // Enable rate limiting

	// Per-IP limits (for unauthenticated requests)
	IP struct {
		Download RateLimitConfig `yaml:"download"` // GET requests for blobs
		Upload   RateLimitConfig `yaml:"upload"`   // PUT/POST requests
		General  RateLimitConfig `yaml:"general"`  // All other requests
	} `yaml:"ip"`

	// Per-pubkey limits (for authenticated requests)
	Pubkey struct {
		Download RateLimitConfig `yaml:"download"` // GET requests for blobs
		Upload   RateLimitConfig `yaml:"upload"`   // PUT/POST requests
		General  RateLimitConfig `yaml:"general"`  // All other requests
	} `yaml:"pubkey"`

	// Bandwidth limits
	Bandwidth struct {
		DownloadMBPerMinute int `yaml:"download_mb_per_minute"` // Max download MB per minute
		UploadMBPerMinute   int `yaml:"upload_mb_per_minute"`   // Max upload MB per minute
	} `yaml:"bandwidth"`

	// Whitelist of pubkeys exempt from rate limiting
	WhitelistedPubkeys []string `yaml:"whitelisted_pubkeys"`
}

type Config struct {
	// Legacy field for backwards compatibility - use Database.SQLite.Path instead
	DbPath             string              `yaml:"db_path"`
	LogLevel           string              `yaml:"log_level"`
	ApiAddr            string              `yaml:"api_addr"`
	CdnUrl             string              `yaml:"cdn_url"`
	AdminPubkey        string              `yaml:"admin_pubkey"`
	MaxUploadSizeBytes int                 `yaml:"max_upload_size_bytes"`
	AccessControlRules []AccessControlRule `yaml:"access_control_rules"`
	AllowedMimeTypes   []string            `yaml:"allowed_mime_types"`

	// New configuration sections
	Storage       StorageConfig        `yaml:"storage"`
	Database      DatabaseConfig       `yaml:"database"`
	Quota         QuotaConfig          `yaml:"quota"`
	Cache         CacheConfig          `yaml:"cache"`
	Encryption    EncryptionConfig     `yaml:"encryption"`
	CDN           CDNConfig            `yaml:"cdn"`
	RateLimiting  RateLimitingConfig   `yaml:"rate_limiting"`
	IPFS          IPFSConfig           `yaml:"ipfs"`
	Transcoding   TranscodingConfig    `yaml:"transcoding"`
	ChunkedUpload ChunkedUploadConfig  `yaml:"chunked_upload"`
	Platform      PlatformConfig       `yaml:"platform"`
}

func NewConfig(path string) (*Config, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Expand environment variables in config
	expanded := os.ExpandEnv(string(bytes))

	config := &Config{}
	err = yaml.Unmarshal([]byte(expanded), config)
	if err != nil {
		return nil, err
	}

	// Apply defaults and backwards compatibility
	config.applyDefaults()

	return config, nil
}

// applyDefaults sets default values and handles backwards compatibility.
func (c *Config) applyDefaults() {
	// Backwards compatibility: if legacy db_path is set but Database is not configured
	if c.DbPath != "" && c.Database.Driver == "" {
		c.Database.Driver = "sqlite"
		c.Database.SQLite.Path = c.DbPath
	}

	// Default database driver
	if c.Database.Driver == "" {
		c.Database.Driver = "sqlite"
	}

	// Default SQLite path
	if c.Database.Driver == "sqlite" && c.Database.SQLite.Path == "" {
		c.Database.SQLite.Path = "db/database.sqlite3"
	}

	// Default PostgreSQL settings
	if c.Database.Postgres.Port == 0 {
		c.Database.Postgres.Port = 5432
	}
	if c.Database.Postgres.SSLMode == "" {
		c.Database.Postgres.SSLMode = "prefer"
	}

	// Default storage backend
	if c.Storage.Backend == "" {
		c.Storage.Backend = "local"
	}

	// Default local storage path
	if c.Storage.Local.Path == "" {
		c.Storage.Local.Path = "data/blobs"
	}

	// Default S3 settings
	if c.Storage.S3.Region == "" {
		c.Storage.S3.Region = "us-east-1"
	}

	// Default quota settings
	if c.Quota.DefaultBytes == 0 {
		c.Quota.DefaultBytes = 1 * 1024 * 1024 * 1024 // 1 GB
	}
	if c.Quota.MaxBytes == 0 {
		c.Quota.MaxBytes = 100 * 1024 * 1024 * 1024 // 100 GB
	}
	if c.Quota.WarnThreshold == 0 {
		c.Quota.WarnThreshold = 80
	}

	// Default CDN settings
	if c.CDN.PresignedExpiry == "" {
		c.CDN.PresignedExpiry = "1h"
	}
	if c.CDN.CacheControl == "" {
		c.CDN.CacheControl = "public, max-age=31536000" // 1 year for immutable content
	}

	// Default rate limiting settings
	if c.RateLimiting.IP.Download.Requests == 0 {
		c.RateLimiting.IP.Download.Requests = 100
		c.RateLimiting.IP.Download.Window = "1m"
	}
	if c.RateLimiting.IP.Upload.Requests == 0 {
		c.RateLimiting.IP.Upload.Requests = 10
		c.RateLimiting.IP.Upload.Window = "1m"
	}
	if c.RateLimiting.IP.General.Requests == 0 {
		c.RateLimiting.IP.General.Requests = 60
		c.RateLimiting.IP.General.Window = "1m"
	}
	if c.RateLimiting.Pubkey.Download.Requests == 0 {
		c.RateLimiting.Pubkey.Download.Requests = 200
		c.RateLimiting.Pubkey.Download.Window = "1m"
	}
	if c.RateLimiting.Pubkey.Upload.Requests == 0 {
		c.RateLimiting.Pubkey.Upload.Requests = 30
		c.RateLimiting.Pubkey.Upload.Window = "1m"
	}
	if c.RateLimiting.Pubkey.General.Requests == 0 {
		c.RateLimiting.Pubkey.General.Requests = 120
		c.RateLimiting.Pubkey.General.Window = "1m"
	}
	if c.RateLimiting.Bandwidth.DownloadMBPerMinute == 0 {
		c.RateLimiting.Bandwidth.DownloadMBPerMinute = 100 // 100 MB/min default
	}
	if c.RateLimiting.Bandwidth.UploadMBPerMinute == 0 {
		c.RateLimiting.Bandwidth.UploadMBPerMinute = 50 // 50 MB/min default
	}

	// Default IPFS settings
	if c.IPFS.GatewayURL == "" {
		c.IPFS.GatewayURL = "https://ipfs.io/ipfs/"
	}

	// Default transcoding settings
	if c.Transcoding.HWAccel.Type == "" {
		c.Transcoding.HWAccel.Type = "none" // Default to software encoding
	}

	// Default chunked upload settings
	if c.ChunkedUpload.DefaultChunkSize == 0 {
		c.ChunkedUpload.DefaultChunkSize = 5 * 1024 * 1024 // 5MB
	}
	if c.ChunkedUpload.MinChunkSize == 0 {
		c.ChunkedUpload.MinChunkSize = 1 * 1024 * 1024 // 1MB
	}
	if c.ChunkedUpload.MaxChunkSize == 0 {
		c.ChunkedUpload.MaxChunkSize = 100 * 1024 * 1024 // 100MB
	}
	if c.ChunkedUpload.MaxSessionTTL == "" {
		c.ChunkedUpload.MaxSessionTTL = "24h"
	}
	if c.ChunkedUpload.DefaultSessionTTL == "" {
		c.ChunkedUpload.DefaultSessionTTL = "1h"
	}
	if c.ChunkedUpload.TempDir == "" {
		c.ChunkedUpload.TempDir = "/tmp/blossom-chunks"
	}

	// Default platform settings (standalone mode for backwards compatibility)
	if c.Platform.Mode == "" {
		c.Platform.Mode = "standalone"
	}
	if c.Platform.ServiceID == "" {
		c.Platform.ServiceID = "blossom"
	}
}

// GetDatabasePath returns the SQLite database path for backwards compatibility.
func (c *Config) GetDatabasePath() string {
	if c.Database.SQLite.Path != "" {
		return c.Database.SQLite.Path
	}
	return c.DbPath
}

// ApplyDefaults applies default values to the config.
// This is called automatically by NewConfig, but can be called manually
// when creating config structs directly (e.g., in tests).
func (c *Config) ApplyDefaults() {
	c.applyDefaults()
}

// IsPlatformMode returns true if running in unified platform mode.
func (c *Config) IsPlatformMode() bool {
	return c.Platform.Mode == "platform"
}

// IsStandaloneMode returns true if running in standalone mode.
func (c *Config) IsStandaloneMode() bool {
	return c.Platform.Mode == "" || c.Platform.Mode == "standalone"
}
