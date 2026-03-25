package database

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/CREATE_TABLES.sql
var createTablesSQL string

type Store struct {
	pool *pgxpool.Pool
}

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

func (db *Store) Close() {
	log.Println("[INFO] Closing database connection...")
	db.pool.Close()
}

// RunMigrations creates database tables if they do not exist yet.
func (db *Store) RunMigrations(ctx context.Context) error {
	// check if tables already exist (checks packages table)
	var exists bool
	err := db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'packages')").Scan(&exists)
	if err != nil {
		return fmt.Errorf("database: migration check failed: %w", err)
	}

	if exists {
		// skip creating tables
		log.Println("[INFO] database: tables already exist, skipping migration ...")
		return nil
	}

	// create tables
	log.Println("[INFO] database: creating tables...")
	_, err = db.pool.Exec(ctx, createTablesSQL)
	if err != nil {
		return fmt.Errorf("database: migration failed: %w", err)
	}
	log.Println("[INFO] database: tables created successfully")
	return nil
}
