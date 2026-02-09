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
	Storage    StorageConfig    `yaml:"storage"`
	Database   DatabaseConfig   `yaml:"database"`
	Quota      QuotaConfig      `yaml:"quota"`
	Cache      CacheConfig      `yaml:"cache"`
	Encryption EncryptionConfig `yaml:"encryption"`
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
