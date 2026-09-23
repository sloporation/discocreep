// Package database is the bot's single point of contact with MariaDB / MySQL.
//
// It owns:
//   - the *sql.DB connection pool;
//   - the embedded migration files under ./migrations (compiled into the binary);
//   - the migration runner that's expected to be called once during startup.
//
// Other packages should depend on this package for DB access rather than
// importing database/sql directly, so that connection lifecycle and migration
// state stay in one place.
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

// migrationsFS holds every .sql file in ./migrations as a virtual filesystem
// inside the compiled binary. Adding a new migration is just dropping a file
// in that directory and rebuilding.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps the sql.DB pool. It's a thin struct today, but keeping it as a
// dedicated type means we can attach helper methods (transactions, named
// queries, instrumentation) here later without touching every caller.
type DB struct {
	*sql.DB
}

// Open dials the database, verifies the connection with a Ping, and configures
// the pool. The returned *DB is safe for concurrent use across goroutines.
func Open(cfg config.DBConfig) (*DB, error) {
	dsn := (&mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		DBName:               cfg.Name,
		ParseTime:            true,
		AllowNativePasswords: true,
		// MultiStatements lets migration files contain multiple ; -separated
		// statements without us having to split them ourselves. golang-migrate
		// requires this for its mysql driver to apply multi-statement files.
		MultiStatements: true,
	}).FormatDSN()

	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	if cfg.PoolSize > 0 {
		pool.SetMaxOpenConns(cfg.PoolSize)
		pool.SetMaxIdleConns(cfg.PoolSize)
	}
	pool.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{DB: pool}, nil
}

// Migrate applies every pending migration in the embedded migrations directory.
// It's idempotent: calling Migrate on an up-to-date database is a no-op.
func (db *DB) Migrate() error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}

	driver, err := migratemysql.WithInstance(db.DB, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("mysql driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "mysql", driver)
	if err != nil {
		return fmt.Errorf("migrate.NewWithInstance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Close shuts down the underlying pool. Safe to call multiple times.
func (db *DB) Close() error {
	if db == nil || db.DB == nil {
		return nil
	}
	return db.DB.Close()
}
