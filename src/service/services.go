package service

import (
	"context"
	"database/sql"
	"fmt"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"go.uber.org/zap"
)

type services struct {
	blobs      core.BlobStorage
	acrs       core.ACRStorage
	mimes      core.MimeTypeService
	settings   core.SettingService
	stats      core.StatService
	quota      core.QuotaService
	moderation core.ModerationService
	media      core.MediaService
	video      core.VideoService
	cdn        core.CDNService
	cache      cache.Cache
	conf       *config.Config
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

	acrService, err := NewACRService(
		conf,
		log,
	)
	if err != nil {
		log.Fatal(err.Error())
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

	quotaService, err := NewQuotaService(queries, &conf.Quota, log)
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
		CDNBaseUrl: conf.CdnUrl,
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

	return &services{
		blobs:      blobService,
		acrs:       acrService,
		mimes:      mimeTypeService,
		settings:   settingsService,
		stats:      statService,
		quota:      quotaService,
		moderation: moderationService,
		media:      mediaService,
		video:      videoService,
		cdn:        cdnService,
		cache:      appCache,
		conf:       conf,
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

func (s *services) Cache() cache.Cache {
	return s.cache
}

func (s *services) Init(ctx context.Context) error {
	return nil
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
