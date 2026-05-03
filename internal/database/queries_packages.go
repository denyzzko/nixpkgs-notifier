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
)

// QueryAllPackages retrieves all packages from database.
func (db *Store) QueryAllPackages(ctx context.Context) ([]Package, error) {
	rows, err := db.pool.Query(ctx, qGetAllPackages)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllPackages: query error: %w", err)
	}
	defer rows.Close()

	// loop through rows and store results
	var packages []Package
	for rows.Next() {
		var p Package
		err := rows.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.LastCheckedAt, &p.Name, &p.Branch, &p.CurrentVersion)
		if err != nil {
			return nil, fmt.Errorf("database.QueryAllPackages: scan error: %w", err)
		}
		packages = append(packages, p)
	}

	// check for overall query error, results may be incomplete
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllPackages: incomplete results: %w", err)
	}

	return packages, nil
}

// QueryPackage retrieves package identified by id.
func (db *Store) QueryPackage(ctx context.Context, packageID int64) (Package, error) {
	var pckg Package
	row := db.pool.QueryRow(ctx, qGetPackage, packageID)
	err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.LastCheckedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Package{}, ErrNotFound
		}
		return Package{}, fmt.Errorf("database.QueryPackage: error querying package (id=%d): %w", packageID, err)
	}

	return pckg, nil
}

// QueryPackageByNameAndBranch retrieves package identified by its name and branch.
func (db *Store) QueryPackageByNameAndBranch(ctx context.Context, name string, branch string) (Package, error) {
	var pckg Package
	row := db.pool.QueryRow(ctx, qGetPackageByNameAndBranch, name, branch)
	err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.LastCheckedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Package{}, ErrNotFound
		}
		return Package{}, fmt.Errorf("database.QueryPackageByNameAndBranch: error querying package (name=%q, branch=%q): %w", name, branch, err)
	}

	return pckg, nil
}

// UpdatePackageLastCheckedAt updates last_checked_at timestamp for a package.
// Called after every nix eval (regardless of whether the version changed).
func (db *Store) UpdatePackageLastCheckedAt(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, sUpdatePackageLastCheckedAt, packageID)
	if err != nil {
		return fmt.Errorf("database.UpdatePackageLastCheckedAt: error updating package (id=%d): %w", packageID, err)
	}
	return nil
}

// StorePackage inserts or updates package in database.
// Returns ID of the created/updated package (updated if version changed).
func (db *Store) StorePackage(ctx context.Context, name string, branch string, version string) (int64, error) {
	var id int64
	err := db.pool.QueryRow(ctx, sInsertPackage, name, branch, version).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("database.StorePackage: error storing package (name=%q, branch=%q): %w", name, branch, err)
	}

	return id, nil
}

// DeletePackage deletes a package by ID (used to rollback a newly created package when nix eval fails).
func (db *Store) DeletePackage(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, dRemovePackage, packageID)
	if err != nil {
		return fmt.Errorf("database.DeletePackage: error deleting package (id=%d): %w", packageID, err)
	}
	return nil
}

// DeleteOrphanPackage deletes a package row only if no trackings or watchlist rows reference it.
func (db *Store) DeleteOrphanPackage(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, dRemoveOrphanPackage, packageID)
	if err != nil {
		return fmt.Errorf("database.DeleteOrphanPackage: %w", err)
	}
	return nil
}
