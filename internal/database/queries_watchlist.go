// Package database provides the data access layer for application.
//
// It is organised in these files:
//   - database.go:              opens connection pool and runs migrations
//   - embeds.go:                embeds all SQL files into the binary at compile time
//   - models.go:                defines data types returned by queries
//   - queries_channels.go:      notification channel operations
//   - queries_config.go:        system configuration operations
//   - queries_helpers.go:       shared helpers and sentinel errors used across query files
//   - queries_notifications.go: notification creation and delivery operations
//   - queries_packages.go:      package and tracking operations
//   - queries_users.go:         user and account operations
//   - queries_watchlist.go:     watchlist operations
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// QueryWatchlistByUserID returns all watchlist entries for a user.
func (db *Store) QueryWatchlistByUserID(ctx context.Context, userID int64) ([]WatchlistEntry, error) {
	rows, err := db.pool.Query(ctx, qGetWatchlistByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryWatchlistByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()
	var entries []WatchlistEntry
	for rows.Next() {
		var e WatchlistEntry
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.UserID, &e.Name, &e.Branch); err != nil {
			return nil, fmt.Errorf("database.QueryWatchlistByUserID: scan error: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryWatchlistByUserID: incomplete results: %w", err)
	}
	return entries, nil
}

// QueryWatchlistEntry returns a single watchlist entry by ID scoped to a user.
func (db *Store) QueryWatchlistEntry(ctx context.Context, id int64, userID int64) (WatchlistEntry, error) {
	var e WatchlistEntry
	row := db.pool.QueryRow(ctx, qGetWatchlistEntryByID, id, userID)
	if err := row.Scan(&e.ID, &e.CreatedAt, &e.UserID, &e.Name, &e.Branch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WatchlistEntry{}, ErrNotFound
		}
		return WatchlistEntry{}, fmt.Errorf("database.QueryWatchlistEntry: error (id=%d, userID=%d): %w", id, userID, err)
	}
	return e, nil
}

// QueryDistinctWatchlistPackages returns all distinct (name, branch) pairs currently in the watchlist.
// Used by scheduler to check packages from watchlist.
func (db *Store) QueryDistinctWatchlistPackages(ctx context.Context) ([]DistinctWatchlistEntry, error) {
	rows, err := db.pool.Query(ctx, qGetDistinctWatchlistNameBranch)
	if err != nil {
		return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: query error: %w", err)
	}
	defer rows.Close()
	var entries []DistinctWatchlistEntry
	for rows.Next() {
		var e DistinctWatchlistEntry
		if err := rows.Scan(&e.Name, &e.Branch); err != nil {
			return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: scan error: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: incomplete results: %w", err)
	}
	return entries, nil
}

// CreateWatchlistEntry inserts a new watchlist entry for user.
// Returns ErrConflict if user is already watching it.
func (db *Store) CreateWatchlistEntry(ctx context.Context, userID int64, name string, branch string) (WatchlistEntry, error) {
	var e WatchlistEntry
	e.UserID = userID
	e.Name = name
	e.Branch = branch
	if err := db.pool.QueryRow(ctx, sInsertWatchlistEntry, userID, name, branch).Scan(&e.ID, &e.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return WatchlistEntry{}, ErrConflict
		}
		return WatchlistEntry{}, fmt.Errorf("database.CreateWatchlistEntry: error (userID=%d, name=%q, branch=%q): %w", userID, name, branch, err)
	}
	return e, nil
}

// DeleteWatchlistEntry removes watchlist entry of the given user.
func (db *Store) DeleteWatchlistEntry(ctx context.Context, id int64, userID int64) error {
	result, err := db.pool.Exec(ctx, dRemoveWatchlistEntry, id, userID)
	if err != nil {
		return fmt.Errorf("database.DeleteWatchlistEntry: error (id=%d, userID=%d): %w", id, userID, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PromoteWatchlistEntries atomically:
//  1. Creates (or updates) package row with confirmed version.
//  2. Deletes all watchlist rows for (name, branch) while collecting user IDs via RETURNING.
//  3. Inserts tracking row for every collected user ID.
//
// Returns package ID and the list of user IDs whose trackings were created,
// so caller can fire CreatePendingNotificationsFirstAppearance.
func (db *Store) PromoteWatchlistEntries(ctx context.Context, name, branch, version string) (int64, []int64, error) {
	// begin transaction
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// update/create package row
	var packageID int64
	if err := tx.QueryRow(ctx, sInsertPackage, name, branch, version).Scan(&packageID); err != nil {
		return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: upsert package: %w", err)
	}

	// delete all watchlist entries and collect user IDs
	wRows, err := tx.Query(ctx, dRemoveWatchlistByNameBranch, name, branch)
	if err != nil {
		return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: delete watchlist: %w", err)
	}
	var userIDs []int64
	for wRows.Next() {
		var wID, uID int64
		if err := wRows.Scan(&wID, &uID); err != nil {
			wRows.Close()
			return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: scan row: %w", err)
		}
		userIDs = append(userIDs, uID)
	}
	wRows.Close()
	if err := wRows.Err(); err != nil {
		return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: rows err: %w", err)
	}

	// insert tracking row for users that were watching this package
	for _, uID := range userIDs {
		if _, err := tx.Exec(ctx, sInsertTracking, uID, packageID, version); err != nil {
			return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: insert tracking (userID=%d): %w", uID, err)
		}
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, fmt.Errorf("database.PromoteWatchlistEntries: commit: %w", err)
	}
	return packageID, userIDs, nil
}
