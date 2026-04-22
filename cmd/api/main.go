package main

import (
	"context"
	"log"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	ginApi "git.aegis-hq.xyz/coldforge/cloistr-blossom/api/gin"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/logging"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf, err := config.NewConfig("config.yml")
	if err != nil {
		log.Fatalf("new config: %v", err)
	}

	logger, err := logging.NewLog(conf.LogLevel)
	if err != nil {
		log.Fatalf("new logger: %v", err)
	}

	// Initialize cache (optional Redis/Dragonfly, falls back to in-memory)
	var appCache cache.Cache
	if conf.Cache.URL != "" {
		redisCache, err := cache.NewRedisCache(conf.Cache.URL, "blossom:")
		if err != nil {
			logger.Warn("failed to connect to cache, using in-memory fallback: " + err.Error())
			appCache = cache.NewMemoryCache(100 * 1024 * 1024)
		} else {
			logger.Info("connected to cache: " + conf.Cache.URL)
			appCache = redisCache
		}
	}

	// Initialize database with new configuration
	dbConfig := db.DBConfig{
		Driver:   conf.Database.Driver,
		Host:     conf.Database.Postgres.Host,
		Port:     conf.Database.Postgres.Port,
		User:     conf.Database.Postgres.User,
		Password: conf.Database.Postgres.Password,
		Database: conf.Database.Postgres.Database,
		SSLMode:  conf.Database.Postgres.SSLMode,
	}

	// For SQLite, use the path as DSN
	if conf.Database.Driver == "sqlite" || conf.Database.Driver == "" {
		dbConfig.Driver = "sqlite"
		dbConfig.DSN = conf.GetDatabasePath()
	}

	database, err := db.NewDBWithConfig(dbConfig, "db/migrations")
	if err != nil {
		logger.Fatal(err.Error())
	}
	queries := db.New(database)

	services := service.New(
		ctx,
		database,
		queries,
		conf,
		appCache,
		logger,
	)
	if err := services.Init(ctx); err != nil {
		logger.Error(err.Error())
	}

	// Initialize metrics with default label values (so they show 0 instead of "no data")
	metrics.Init()

	// Start background metrics updater
	go updateMetricsPeriodically(ctx, services)

	api := ginApi.SetupRoutes(
		services,
		conf.CdnUrl,
		conf.AdminPubkey,
		conf,
		appCache,
		logger,
	)
	api.Run(conf.ApiAddr)
}

// updateMetricsPeriodically updates Prometheus gauges with current stats
func updateMetricsPeriodically(ctx context.Context, services core.Services) {
	// Update immediately on startup
	updateMetrics(ctx, services)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateMetrics(ctx, services)
		}
	}
}

func updateMetrics(ctx context.Context, services core.Services) {
	stats, err := services.Stats().Get(ctx)
	if err != nil {
		return
	}

	metrics.StorageBytes.Set(float64(stats.BytesStored))
	metrics.StoredBlobs.Set(float64(stats.BlobCount))
	metrics.ActiveUsers.Set(float64(stats.PubkeyCount))
}
