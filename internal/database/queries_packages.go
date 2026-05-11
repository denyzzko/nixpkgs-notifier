package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// QueryAllPackagesPaged retrieves one page of tracked and watched packages for user ordered alphabetically by name.
func (db *Store) QueryAllPackagesPaged(ctx context.Context, userID int64, limit int, offset int) ([]PackageRow, error) {
	rows, err := db.pool.Query(ctx, qGetAllPackagesPaged, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllPackagesPaged: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var result []PackageRow
	for rows.Next() {
		var r PackageRow
		err := rows.Scan(
			&r.Kind, &r.PackageID, &r.Name, &r.Branch,
			&r.LastNotifiedVersion, &r.LastCheckedAt, &r.CurrentVersion,
			&r.WatchlistID,
		)
		if err != nil {
			return nil, fmt.Errorf("database.QueryAllPackagesPaged: scan error: %w", err)
		}
		result = append(result, r)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllPackagesPaged: incomplete results: %w", err)
	}
	return result, nil
}

// CountAllPackages returns total number of tracked + watched packages for a user.
func (db *Store) CountAllPackages(ctx context.Context, userID int64) (int64, error) {
	var count int64
	err := db.pool.QueryRow(ctx, qCountAllPackages, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("database.CountAllPackages: query error (userID=%d): %w", userID, err)
	}
	return count, nil
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

// QueryAllTrackedPackages retrieves all packages that have at least one tracking entry.
func (db *Store) QueryAllTrackedPackages(ctx context.Context) ([]Package, error) {
	rows, err := db.pool.Query(ctx, qGetAllTrackedPackages)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllTrackedPackages: query error: %w", err)
	}
	defer rows.Close()

	var packages []Package
	for rows.Next() {
		var p Package
		err := rows.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.LastCheckedAt, &p.Name, &p.Branch, &p.CurrentVersion)
		if err != nil {
			return nil, fmt.Errorf("database.QueryAllTrackedPackages: scan error: %w", err)
		}
		packages = append(packages, p)
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllTrackedPackages: incomplete results: %w", err)
	}

	return packages, nil
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
