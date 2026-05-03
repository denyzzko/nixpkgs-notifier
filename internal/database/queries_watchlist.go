// Package database provides the data access layer for application.
//
// It is organised in these files:
//   - database.go:              opens connection pool and runs migrations
//   - embeds.go:                embeds all SQL files into the binary at compile time
//   - models.go:                defines data types returned by queries
//   - queries_channels.go:      notification channel operations
//   - queries_check_state.go:   check state operations (pending/done/failed/not_found rows written by check goroutines and read by polling endpoints)
//   - queries_config.go:        system configuration operations
//   - queries_helpers.go:       shared helpers and sentinel errors used across query files
//   - queries_notifications.go: notification creation and delivery operations
//   - queries_packages.go:      package  operations
//   - queries_trackings.go:      tracking operations
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

// QueryUsersWatchedPackages returns all watched packages for user.
func (db *Store) QueryUsersWatchedPackages(ctx context.Context, userID int64) ([]WatchedPackage, error) {
	rows, err := db.pool.Query(ctx, qGetUsersWatchedPackages, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryUsersWatchedPackages: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	// loop through rows and store results
	var entries []WatchedPackage
	for rows.Next() {
		var e WatchedPackage
		err := rows.Scan(&e.WatchlistID, &e.CreatedAt, &e.UserID, &e.PackageID, &e.Name, &e.Branch)
		if err != nil {
			return nil, fmt.Errorf("database.QueryUsersWatchedPackages: scan error: %w", err)
		}
		entries = append(entries, e)
	}

	// check for overall query error, results may be incomplete
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryUsersWatchedPackages: incomplete results: %w", err)
	}
	return entries, nil
}

// QueryWatchlistEntry returns a single watched package by watchlist ID.
func (db *Store) QueryWatchlistEntry(ctx context.Context, id int64, userID int64) (WatchedPackage, error) {
	var e WatchedPackage
	row := db.pool.QueryRow(ctx, qGetWatchlistEntryByID, id, userID)
	err := row.Scan(&e.WatchlistID, &e.CreatedAt, &e.UserID, &e.PackageID, &e.Name, &e.Branch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WatchedPackage{}, ErrNotFound
		}
		return WatchedPackage{}, fmt.Errorf("database.QueryWatchlistEntry: error (id=%d, userID=%d): %w", id, userID, err)
	}
	return e, nil
}

// QueryDistinctWatchlistPackages returns all distinct packages currently in watchlist.
// Used by the background scheduler to check all watched packages.
func (db *Store) QueryDistinctWatchlistPackages(ctx context.Context) ([]DistinctWatchlistEntry, error) {
	rows, err := db.pool.Query(ctx, qGetDistinctWatchlistNameBranch)
	if err != nil {
		return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: query error: %w", err)
	}
	defer rows.Close()
	var entries []DistinctWatchlistEntry
	for rows.Next() {
		var e DistinctWatchlistEntry
		err := rows.Scan(&e.PackageID, &e.Name, &e.Branch)
		if err != nil {
			return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: scan error: %w", err)
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryDistinctWatchlistPackages: incomplete results: %w", err)
	}
	return entries, nil
}

// CreateWatchlistEntry inserts new watchlist row for existing package.
// The package must already exist in packages table (possibly with current_version = "").
// Returns ErrConflict (sql code 23505) if user is already watching this package.
func (db *Store) CreateWatchlistEntry(ctx context.Context, userID int64, packageID int64) (WatchlistEntry, error) {
	var e WatchlistEntry
	e.UserID = userID
	e.PackageID = packageID
	err := db.pool.QueryRow(ctx, sInsertWatchlistEntry, userID, packageID).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return WatchlistEntry{}, ErrConflict
		}
		return WatchlistEntry{}, fmt.Errorf("database.CreateWatchlistEntry: error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return e, nil
}

// DeleteWatchlistEntry removes watchlist row by its ID.
// Returns package_id of deleted entry.
func (db *Store) DeleteWatchlistEntry(ctx context.Context, id int64, userID int64) (int64, error) {
	var packageID int64
	err := db.pool.QueryRow(ctx, dRemoveWatchlistEntry, id, userID).Scan(&packageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("database.DeleteWatchlistEntry: error (id=%d, userID=%d): %w", id, userID, err)
	}
	return packageID, nil
}

// PromoteWatchlistEntries atomically:
//  1. Sets current_version on already-existing package row.
//  2. Deletes all watchlist rows for this package_id, collecting user IDs via RETURNING.
//  3. Inserts tracking row for each collected user ID.
//
// Returns list of user IDs that were watching so caller can fire notifications for them.
func (db *Store) PromoteWatchlistEntries(ctx context.Context, packageID int64, version string) ([]int64, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("database.PromoteWatchlistEntries: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// set current_version on package row
	_, err = tx.Exec(ctx, sUpdatePackageCurrentVersion, packageID, version)
	if err != nil {
		return nil, fmt.Errorf("database.PromoteWatchlistEntries: set version: %w", err)
	}

	// delete all watchlist entries for this package and collect user IDs
	wRows, err := tx.Query(ctx, dRemoveWatchlistByPackageID, packageID)
	if err != nil {
		return nil, fmt.Errorf("database.PromoteWatchlistEntries: delete watchlist: %w", err)
	}
	var userIDs []int64
	for wRows.Next() {
		var wID, uID int64
		err := wRows.Scan(&wID, &uID)
		if err != nil {
			wRows.Close()
			return nil, fmt.Errorf("database.PromoteWatchlistEntries: scan row: %w", err)
		}
		userIDs = append(userIDs, uID)
	}
	wRows.Close()
	err = wRows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.PromoteWatchlistEntries: rows err: %w", err)
	}

	// insert tracking row for each user that was watching this package
	for _, uID := range userIDs {
		_, err := tx.Exec(ctx, sInsertTracking, uID, packageID, version)
		if err != nil {
			return nil, fmt.Errorf("database.PromoteWatchlistEntries: insert tracking (userID=%d): %w", uID, err)
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("database.PromoteWatchlistEntries: commit: %w", err)
	}
	return userIDs, nil
}
