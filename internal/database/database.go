// Package database provides the data access layer for application.
//
// It is organised in four files:
//   - database.go: opens connection pool and runs migrations
//   - embeds.go:   embeds all SQL files into the binary at compile time
//   - models.go:   defines data types returned by queries
//   - queries.go:  implements all database operations (using embedded SQL)
package database

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Store wraps a pgxpool connection pool and is the receiver for all database operations.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates a new connection pool for given DSN, verifies it with ping and returns a Store.
func Open(ctx context.Context, dsn string) (*Store, error) {
	// parse config
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// adjust config
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// create connection
	dbpool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// check connection with ping
	if err := dbpool.Ping(ctx); err != nil {
		dbpool.Close()
		return nil, err
	}

	return &Store{pool: dbpool}, nil
}

// Close closes the underlying connection pool.
func (db *Store) Close() {
	log.Println("[INFO] Closing database connection...")
	db.pool.Close()
}

// RunMigrations applies all pending database migrations using goose library.
func (db *Store) RunMigrations(ctx context.Context) error {
	// wrap pgxpool into a *sql.DB that goose understands (does not open new connection, just reuses existing pool)
	sqlDB := stdlib.OpenDBFromPool(db.pool)
	defer sqlDB.Close()

	// tell goose to load migration files from the embedded filesystem
	goose.SetBaseFS(migrationFS)

	// set the SQL dialect so goose uses PostgreSQL syntax
	err := goose.SetDialect("postgres")
	if err != nil {
		return fmt.Errorf("database: goose dialect: %w", err)
	}

	// apply all pending migrations in order
	err = goose.UpContext(ctx, sqlDB, "sql/migrations")
	if err != nil {
		return fmt.Errorf("database: migration failed: %w", err)
	}

	return nil
}
