package service

import (
	"context"
	"database/sql"
	"fmt"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cashu"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/lightning"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
	"go.uber.org/zap"
)

type services struct {
	blobs         core.BlobStorage
	acrs          core.ACRStorage
	mimes         core.MimeTypeService
	settings      core.SettingService
	stats         core.StatService
	quota         core.QuotaService
	moderation    core.ModerationService
	media         core.MediaService
	video         core.VideoService
	cdn           core.CDNService
	ipfs          core.IPFSService
	torrent       core.TorrentService
	chunkedUpload core.ChunkedUploadService
	notifications core.NotificationService
	expiration    core.ExpirationService
	replication   core.ReplicationService
	batch         core.BatchService
	aiModeration  core.AIModerationService
	federation    core.FederationService
	analytics     core.AnalyticsService
	payment       core.PaymentService
	cache         cache.Cache
	conf          *config.Config
}

func New(
	ctx context.Context,
	database *sql.DB,
	queries *db.Queries,
	conf *config.Config,
	appCache cache.Cache,
	log *zap.Logger,
) core.Services {
	// Initialize storage backend
	storageBackend, err := initStorageBackend(ctx, conf, log)
	if err != nil {
		log.Fatal("failed to initialize storage backend", zap.Error(err))
	}

	// Initialize encryption if configured
	var encryptor *encryption.Encryptor
	if conf.Encryption.Enabled && conf.Encryption.MasterKey != "" {
		encryptor, err = encryption.NewEncryptor(conf.Encryption.MasterKey)
		if err != nil {
			log.Fatal("failed to initialize encryption", zap.Error(err))
		}
		log.Info("server-side encryption enabled")
	} else {
		log.Info("server-side encryption disabled")
	}

	blobService, err := NewBlobService(
		database,
		queries,
		storageBackend,
		encryptor,
		conf.CdnUrl,
		log,
	)
	if err != nil {
		log.Fatal(err.Error())
	}

	// Initialize ACR and Quota services based on platform mode
	var acrService core.ACRStorage
	var quotaService core.QuotaService

	if conf.IsPlatformMode() {
		// Platform mode: use unified platform database
		log.Info("initializing services in platform mode",
			zap.String("database_url", maskDatabaseURL(conf.Platform.DatabaseURL)),
			zap.String("service_id", conf.Platform.ServiceID))

		platformClient, err := platform.NewClient(platform.Config{
			Mode:        platform.ModePlatform,
			DatabaseURL: conf.Platform.DatabaseURL,
			ServiceID:   conf.Platform.ServiceID,
		})
		if err != nil {
			log.Fatal("failed to initialize platform client", zap.Error(err))
		}

		acrService, err = NewPlatformACRService(platformClient, log)
		if err != nil {
			log.Fatal("failed to initialize platform ACR service", zap.Error(err))
		}

		quotaService, err = NewPlatformQuotaService(
			platformClient,
			conf.Quota.DefaultBytes,
			conf.Quota.MaxBytes,
			conf.Quota.Enabled,
			log,
		)
		if err != nil {
			log.Fatal("failed to initialize platform quota service", zap.Error(err))
		}
	} else {
		// Standalone mode: use local config and database
		log.Info("initializing services in standalone mode")

		acrService, err = NewACRService(conf, log)
		if err != nil {
			log.Fatal(err.Error())
		}

		quotaService, err = NewQuotaService(queries, &conf.Quota, log)
		if err != nil {
			log.Fatal(err.Error())
		}
	}

	settingsService, err := NewSettingService(
		conf.MaxUploadSizeBytes,
	)
	if err != nil {
		log.Fatal(err.Error())
	}

	mimeTypeService, err := NewMimeTypeService(
		ctx,
		queries,
		conf,
		log,
	)
	if err != nil {
		log.Fatal(err.Error())
	}

	statService, err := NewStatService(queries)
	if err != nil {
		log.Fatal(err.Error())
	}

	moderationService, err := NewModerationService(queries, blobService, quotaService, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	// Default to in-memory cache if none provided
	if appCache == nil {
		appCache = cache.NewMemoryCache(100 * 1024 * 1024) // 100MB
	}

	mediaService, err := NewMediaService(storageBackend, appCache, DefaultMediaConfig(), log)
	if err != nil {
		log.Fatal(err.Error())
	}

	videoService, err := NewVideoService(storageBackend, appCache, VideoConfig{
		WorkDir:    conf.Transcoding.WorkDir,
		FFmpegPath: conf.Transcoding.FFmpegPath,
		CDNBaseUrl: conf.CdnUrl,
		HWAccel: core.HWAccelConfig{
			Type:      core.HWAccelType(conf.Transcoding.HWAccel.Type),
			Device:    conf.Transcoding.HWAccel.Device,
			Preset:    conf.Transcoding.HWAccel.Preset,
			LookAhead: conf.Transcoding.HWAccel.LookAhead,
		},
	}, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	cdnService, err := NewCDNService(storageBackend, CDNServiceConfig{
		CDNConfig:  &conf.CDN,
		CDNBaseURL: conf.CdnUrl,
	}, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	ipfsService, err := NewIPFSService(storageBackend, appCache, &conf.IPFS, log)
	if err != nil {
		log.Fatal(err.Error())
	}

	torrentService := NewTorrentService(storageBackend, appCache, log)

	var chunkedUploadService core.ChunkedUploadService
	if conf.ChunkedUpload.Enabled {
		chunkedUploadService, err = NewChunkedUploadService(
			database,
			queries,
			storageBackend,
			blobService,
			quotaService,
			&conf.ChunkedUpload,
			conf.CdnUrl,
			log,
		)
		if err != nil {
			log.Fatal("failed to initialize chunked upload service", zap.Error(err))
		}
		log.Info("chunked upload service enabled",
			zap.Int64("default_chunk_size", conf.ChunkedUpload.DefaultChunkSize),
			zap.String("temp_dir", conf.ChunkedUpload.TempDir))
	}

	// Initialize notification service for real-time updates
	notificationService := NewNotificationService(log)
	log.Info("notification service initialized")

	// Initialize expiration service
	expirationService := NewExpirationService(queries, storageBackend, core.DefaultExpirationConfig(), log)
	log.Info("expiration service initialized")

	// Initialize replication service (nil if not configured)
	var replicationService core.ReplicationService

	// Initialize batch service
	batchService, err := NewBatchService(
		blobService,
		storageBackend,
		quotaService,
		expirationService,
		core.DefaultBatchConfig(),
		conf.CdnUrl,
		log,
	)
	if err != nil {
		log.Fatal("failed to initialize batch service", zap.Error(err))
	}
	log.Info("batch service initialized")

	// Initialize AI moderation service
	aiModerationService, err := NewAIModerationService(
		core.DefaultAIModerationConfig(),
		appCache,
		moderationService,
		log,
	)
	if err != nil {
		log.Fatal("failed to initialize AI moderation service", zap.Error(err))
	}
	log.Info("AI moderation service initialized")

	// Initialize federation service
	federationService, err := NewFederationService(
		conf.Federation,
		conf.CdnUrl,
		queries,
		storageBackend,
		log,
	)
	if err != nil {
		log.Fatal("failed to initialize federation service", zap.Error(err))
	}
	if conf.Federation.Enabled {
		log.Info("federation service initialized",
			zap.String("mode", conf.Federation.Mode),
			zap.Int("relay_count", len(conf.Federation.RelayURLs)))
	}

	// Initialize analytics service
	analyticsService, err := NewAnalyticsService(queries, log)
	if err != nil {
		log.Fatal("failed to initialize analytics service", zap.Error(err))
	}
	log.Info("analytics service initialized")

	// Initialize payment service (BUD-07)
	var paymentService core.PaymentService
	if conf.Payment.Enabled {
		// Initialize Lightning client if configured
		var lightningClient core.LightningClient
		if conf.Payment.Lightning.Enabled {
			lightningClient, err = lightning.NewClient(&conf.Payment.Lightning, log)
			if err != nil {
				log.Warn("failed to initialize Lightning client, Lightning payments disabled", zap.Error(err))
			} else if lightningClient.IsConnected() {
				log.Info("Lightning client initialized")
			}
		}

		// Initialize Cashu client if configured
		var cashuClient core.CashuClient
		if conf.Payment.Cashu.Enabled {
			cashuClient, err = cashu.NewClient(&conf.Payment.Cashu, log)
			if err != nil {
				log.Warn("failed to initialize Cashu client, Cashu payments disabled", zap.Error(err))
			} else if cashuClient.IsConnected() {
				log.Info("Cashu client initialized")
			}
		}

		paymentService, err = NewPaymentService(&conf.Payment, queries, lightningClient, cashuClient, log)
		if err != nil {
			log.Fatal("failed to initialize payment service", zap.Error(err))
		}
		log.Info("payment service initialized",
			zap.Float64("satoshis_per_byte", conf.Payment.SatoshisPerByte),
			zap.Int64("free_bytes_limit", conf.Payment.FreeBytesLimit),
			zap.Bool("lightning_enabled", conf.Payment.Lightning.Enabled),
			zap.Bool("cashu_enabled", conf.Payment.Cashu.Enabled))
	}

	return &services{
		blobs:         blobService,
		acrs:          acrService,
		mimes:         mimeTypeService,
		settings:      settingsService,
		stats:         statService,
		quota:         quotaService,
		moderation:    moderationService,
		media:         mediaService,
		video:         videoService,
		cdn:           cdnService,
		ipfs:          ipfsService,
		torrent:       torrentService,
		chunkedUpload: chunkedUploadService,
		notifications: notificationService,
		expiration:    expirationService,
		replication:   replicationService,
		batch:         batchService,
		aiModeration:  aiModerationService,
		federation:    federationService,
		analytics:     analyticsService,
		payment:       paymentService,
		cache:         appCache,
		conf:          conf,
	}
}

func (s *services) Blob() core.BlobStorage {
	return s.blobs
}

func (s *services) ACR() core.ACRStorage {
	return s.acrs
}

func (s *services) Mime() core.MimeTypeService {
	return s.mimes
}

func (s *services) Settings() core.SettingService {
	return s.settings
}

func (s *services) Stats() core.StatService {
	return s.stats
}

func (s *services) Quota() core.QuotaService {
	return s.quota
}

func (s *services) Moderation() core.ModerationService {
	return s.moderation
}

func (s *services) Media() core.MediaService {
	return s.media
}

func (s *services) Video() core.VideoService {
	return s.video
}

func (s *services) CDN() core.CDNService {
	return s.cdn
}

func (s *services) IPFS() core.IPFSService {
	return s.ipfs
}

func (s *services) Torrent() core.TorrentService {
	return s.torrent
}

func (s *services) ChunkedUpload() core.ChunkedUploadService {
	return s.chunkedUpload
}

func (s *services) Notifications() core.NotificationService {
	return s.notifications
}

func (s *services) Expiration() core.ExpirationService {
	return s.expiration
}

func (s *services) Replication() core.ReplicationService {
	return s.replication
}

func (s *services) Batch() core.BatchService {
	return s.batch
}

func (s *services) AIModeration() core.AIModerationService {
	return s.aiModeration
}

func (s *services) Federation() core.FederationService {
	return s.federation
}

func (s *services) Analytics() core.AnalyticsService {
	return s.analytics
}

func (s *services) Payment() core.PaymentService {
	return s.payment
}

func (s *services) Cache() cache.Cache {
	return s.cache
}

func (s *services) Init(ctx context.Context) error {
	return nil
}

// maskDatabaseURL masks the password in a database URL for safe logging.
func maskDatabaseURL(url string) string {
	if url == "" {
		return "(not set)"
	}
	// Simple masking - in production, use a proper URL parser
	// This just masks anything between :// and @
	start := 0
	for i := 0; i < len(url)-2; i++ {
		if url[i:i+3] == "://" {
			start = i + 3
			break
		}
	}
	end := len(url)
	for i := start; i < len(url); i++ {
		if url[i] == '@' {
			end = i
			break
		}
	}
	if start > 0 && end < len(url) {
		return url[:start] + "***@" + url[end+1:]
	}
	return url
}

// initStorageBackend creates the appropriate storage backend based on config.
func initStorageBackend(ctx context.Context, conf *config.Config, log *zap.Logger) (storage.StorageBackend, error) {
	switch conf.Storage.Backend {
	case "s3":
		log.Info("initializing S3 storage backend",
			zap.String("endpoint", conf.Storage.S3.Endpoint),
			zap.String("bucket", conf.Storage.S3.Bucket),
			zap.String("region", conf.Storage.S3.Region))

		return storage.NewS3Storage(ctx, storage.S3Config{
			Endpoint:  conf.Storage.S3.Endpoint,
			Bucket:    conf.Storage.S3.Bucket,
			Region:    conf.Storage.S3.Region,
			AccessKey: conf.Storage.S3.AccessKey,
			SecretKey: conf.Storage.S3.SecretKey,
			PathStyle: conf.Storage.S3.PathStyle,
		})

	case "local", "":
		log.Info("initializing local storage backend",
			zap.String("path", conf.Storage.Local.Path))

		return storage.NewLocalStorage(conf.Storage.Local.Path)

	default:
		return nil, fmt.Errorf("unknown storage backend: %s", conf.Storage.Backend)
	}
}
