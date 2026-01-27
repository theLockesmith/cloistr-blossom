package db

import (
	"database/sql"
	"fmt"

	migrate "github.com/rubenv/sql-migrate"
)

// DBConfig holds database connection configuration.
type DBConfig struct {
	Driver   string // "sqlite" or "postgres"
	DSN      string // Connection string
	Host     string // PostgreSQL host
	Port     int    // PostgreSQL port
	User     string // PostgreSQL user
	Password string // PostgreSQL password
	Database string // PostgreSQL database name
	SSLMode  string // PostgreSQL SSL mode
}

// NewDB creates a new database connection with the legacy SQLite-only interface.
// Deprecated: Use NewDBWithConfig instead.
func NewDB(
	path string,
	migrationsPath string,
) (*sql.DB, error) {
	return NewDBWithConfig(DBConfig{
		Driver: "sqlite",
		DSN:    path,
	}, migrationsPath)
}

// NewDBWithConfig creates a new database connection with the given configuration.
func NewDBWithConfig(cfg DBConfig, migrationsPath string) (*sql.DB, error) {
	var dbi *sql.DB
	var err error
	var dialect string

	switch cfg.Driver {
	case "postgres":
		dsn := cfg.DSN
		if dsn == "" {
			dsn = fmt.Sprintf(
				"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
				cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
			)
		}
		dbi, err = sql.Open("postgres", dsn)
		dialect = "postgres"

	case "sqlite", "":
		dbi, err = sql.Open("sqlite3", cfg.DSN)
		dialect = "sqlite3"

	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Run migrations based on dialect
	migrationDir := migrationsPath
	if dialect == "postgres" {
		migrationDir = migrationsPath + "/postgres"
	}

	migrations := &migrate.FileMigrationSource{
		Dir: migrationDir,
	}
	_, err = migrate.Exec(dbi, dialect, migrations, migrate.Up)
	if err != nil {
		dbi.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return dbi, nil
}
