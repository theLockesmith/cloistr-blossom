package main

import (
	"context"
	"log"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	ginApi "git.coldforge.xyz/coldforge/coldforge-blossom/api/gin"
	"git.coldforge.xyz/coldforge/coldforge-blossom/db"
	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/cache"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/pkg/config"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/pkg/logging"
	"git.coldforge.xyz/coldforge/coldforge-blossom/src/service"
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

	api := ginApi.SetupRoutes(
		services,
		conf.CdnUrl,
		conf.AdminPubkey,
		logger,
	)
	api.Run(conf.ApiAddr)
}
