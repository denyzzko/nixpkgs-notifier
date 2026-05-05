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
		err := rows.Scan(&p.PackageID, &p.Name, &p.Branch, &p.LastNotifiedVersion, &p.LastCheckedAt, &p.CurrentVersion)
		if err != nil {
			return nil, fmt.Errorf("database.QueryUsersTrackedPackages: scan error: %w", err)
		}
		trackedPackages = append(trackedPackages, p)
	}

	// check for overall query error, results may be incomplete
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryUsersTrackedPackages: incomplete results: %w", err)
	}

	return trackedPackages, nil
}

// QueryUsersTrackedPackage retrieves a single package from database that user tracks identified by userID and packageID.
func (db *Store) QueryUsersTrackedPackage(ctx context.Context, userID int64, packageID int64) (TrackedPackage, error) {
	var p TrackedPackage

	row := db.pool.QueryRow(ctx, qGetUsersTrackedPackage, userID, packageID)
	err := row.Scan(&p.PackageID, &p.Name, &p.Branch, &p.LastNotifiedVersion, &p.LastCheckedAt, &p.CurrentVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackedPackage{}, ErrNotFound
		}
		return TrackedPackage{}, fmt.Errorf("database.QueryUsersTrackedPackage: error querying tracked package (userID=%d, packageID=%d): %w", userID, packageID, err)
	}

	return p, nil
}

// QueryTracking retrieves tracking record identified by user ID and tracked package ID.
func (db *Store) QueryTracking(ctx context.Context, userID int64, packageID int64) (Tracking, error) {
	var tracking Tracking
	row := db.pool.QueryRow(ctx, qGetTracking, userID, packageID)
	err := row.Scan(&tracking.CreatedAt, &tracking.UpdatedAt, &tracking.UserID, &tracking.PackageID, &tracking.LastNotifiedVersion)
	if err != nil {
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
		err := rows.Scan(&t.UserID, &t.PackageID, &t.LastNotifiedVersion)
		if err != nil {
			return nil, fmt.Errorf("database.QueryTrackingsByPackageID: scan error: %w", err)
		}
		trackings = append(trackings, t)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryTrackingsByPackageID: incomplete results: %w", err)
	}
	return trackings, nil
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
