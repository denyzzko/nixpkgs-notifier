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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// QueryUsersTrackedPackages retrieves all packages from database that user tracks by his ID.
func (db *Store) QueryUsersTrackedPackages(ctx context.Context, userID int64) ([]TrackedPackage, error) {
	rows, err := db.pool.Query(ctx, qGetUsersTrackedPackages, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryUsersTrackedPackages: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	// loop through rows and store results
	var trackedPackages []TrackedPackage
	for rows.Next() {
		var p TrackedPackage
		if err := rows.Scan(&p.PackageID, &p.Name, &p.Branch, &p.LastNotifiedVersion, &p.LastCheckedAt, &p.CurrentVersion); err != nil {
			return nil, fmt.Errorf("database.QueryUsersTrackedPackages: scan error: %w", err)
		}
		trackedPackages = append(trackedPackages, p)
	}

	// check for overall query error, results may be incomplete
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryUsersTrackedPackages: incomplete results: %w", err)
	}

	return trackedPackages, nil
}

// QueryUsersTrackedPackage retrieves a single package from database that user tracks identified by userID and packageID.
func (db *Store) QueryUsersTrackedPackage(ctx context.Context, userID int64, packageID int64) (TrackedPackage, error) {
	var p TrackedPackage

	row := db.pool.QueryRow(ctx, qGetUsersTrackedPackage, userID, packageID)
	if err := row.Scan(&p.PackageID, &p.Name, &p.Branch, &p.LastNotifiedVersion, &p.LastCheckedAt, &p.CurrentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackedPackage{}, ErrNotFound
		}
		return TrackedPackage{}, fmt.Errorf("database.QueryUsersTrackedPackage: error querying tracked package (userID=%d, packageID=%d): %w", userID, packageID, err)
	}

	return p, nil
}

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
		if err := rows.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.LastCheckedAt, &p.Name, &p.Branch, &p.CurrentVersion); err != nil {
			return nil, fmt.Errorf("database.QueryAllPackages: scan error: %w", err)
		}
		packages = append(packages, p)
	}

	// check for overall query error, results may be incomplete
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryAllPackages: incomplete results: %w", err)
	}

	return packages, nil
}

// QueryPackage retrieves package identified by id.
func (db *Store) QueryPackage(ctx context.Context, packageID int64) (Package, error) {
	var pckg Package
	row := db.pool.QueryRow(ctx, qGetPackage, packageID)
	if err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.LastCheckedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion); err != nil {
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
	if err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.LastCheckedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion); err != nil {
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

// QueryTracking retrieves tracking record identified by user ID and tracked package ID.
func (db *Store) QueryTracking(ctx context.Context, userID int64, packageID int64) (Tracking, error) {
	var tracking Tracking
	row := db.pool.QueryRow(ctx, qGetTracking, userID, packageID)
	if err := row.Scan(&tracking.CreatedAt, &tracking.UpdatedAt, &tracking.UserID, &tracking.PackageID, &tracking.LastNotifiedVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Tracking{}, ErrNotFound
		}
		return Tracking{}, fmt.Errorf("database.QueryTracking: error querying tracking (userID=%d, packageID=%d): %w", userID, packageID, err)
	}

	return tracking, nil
}

// QueryTrackingsByPackageID retrieves all trackings rows for a specific package.
func (db *Store) QueryTrackingsByPackageID(ctx context.Context, packageID int64) ([]Tracking, error) {
	rows, err := db.pool.Query(ctx, qGetTrackingsByPackageID, packageID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryTrackingsByPackageID: query error (packageID=%d): %w", packageID, err)
	}
	defer rows.Close()

	var trackings []Tracking
	for rows.Next() {
		var t Tracking
		if err := rows.Scan(&t.UserID, &t.PackageID, &t.LastNotifiedVersion); err != nil {
			return nil, fmt.Errorf("database.QueryTrackingsByPackageID: scan error: %w", err)
		}
		trackings = append(trackings, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryTrackingsByPackageID: incomplete results: %w", err)
	}
	return trackings, nil
}

// StorePackage inserts or updates package in database.
// Returns ID of the created/updated package (updated if version changed).
func (db *Store) StorePackage(ctx context.Context, name string, branch string, version string) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertPackage, name, branch, version).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.StorePackage: error storing package (name=%q, branch=%q): %w", name, branch, err)
	}

	return id, nil
}

// StoreTracking inserts or updates tracking record (updated if version changed).
func (db *Store) StoreTracking(ctx context.Context, userID int64, packageID int64, lastNotifiedVersion string) error {
	_, err := db.pool.Exec(ctx, sInsertTracking, userID, packageID, lastNotifiedVersion)
	if err != nil {
		return fmt.Errorf("database.StoreTracking: error storing tracking (userID=%d, packageID=%d): %w", userID, packageID, err)
	}

	return nil
}

// DeleteTracking deletes tracking identified by user ID and tracked package ID.
func (db *Store) DeleteTracking(ctx context.Context, userID int64, packageID int64) error {
	result, err := db.pool.Exec(ctx, dRemoveTracking, packageID, userID)
	if err != nil {
		return fmt.Errorf("database.DeleteTracking: error deleting tracking (packageID=%d, userID=%d): %w", packageID, userID, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePackage deletes a package by ID (used to rollback a newly created package when nix eval fails).
func (db *Store) DeletePackage(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, dRemovePackage, packageID)
	if err != nil {
		return fmt.Errorf("database.DeletePackage: error deleting package (id=%d): %w", packageID, err)
	}
	return nil
}
