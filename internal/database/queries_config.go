package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// QuerySystemConfig loads settings from database saved by admin at runtime.
// Returns ErrNotFound if admin has never saved config (app should use env defaults).
func (db *Store) QuerySystemConfig(ctx context.Context) (SystemConfig, error) {
	var cfg SystemConfig
	var dispatchNs, checkNs, skipNs int64

	row := db.pool.QueryRow(ctx, qGetSystemConfig)
	err := row.Scan(
		&dispatchNs,
		&cfg.NotificationMaxRetries,
		&cfg.NotificationDisableOnMaxRetries,
		&cfg.NotificationWorkerCount,
		&checkNs,
		&cfg.PackageCheckWorkerCount,
		&skipNs,
		&cfg.NotificationRetentionDays,
		&cfg.MaxWebhooksPerUser,
		&cfg.MaxEmailsPerUser,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SystemConfig{}, ErrNotFound
		}
		return SystemConfig{}, fmt.Errorf("database.QuerySystemConfig: %w", err)
	}

	cfg.NotificationDispatchInterval = time.Duration(dispatchNs)
	cfg.PackageCheckInterval = time.Duration(checkNs)
	cfg.PackageCheckSkipInterval = time.Duration(skipNs)
	return cfg, nil
}

// UpsertSystemConfig saves admin runtime settings to the database.
// Inserts on first call, updates on all next calls (its a single row table).
func (db *Store) UpsertSystemConfig(ctx context.Context, cfg SystemConfig) error {
	_, err := db.pool.Exec(ctx, sUpdateSystemConfig,
		int64(cfg.NotificationDispatchInterval),
		cfg.NotificationMaxRetries,
		cfg.NotificationDisableOnMaxRetries,
		cfg.NotificationWorkerCount,
		int64(cfg.PackageCheckInterval),
		cfg.PackageCheckWorkerCount,
		int64(cfg.PackageCheckSkipInterval),
		cfg.NotificationRetentionDays,
		cfg.MaxWebhooksPerUser,
		cfg.MaxEmailsPerUser,
	)
	if err != nil {
		return fmt.Errorf("database.UpsertSystemConfig: %w", err)
	}
	return nil
}
