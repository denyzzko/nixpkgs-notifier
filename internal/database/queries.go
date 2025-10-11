package database

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type PackageRow struct {
	ID        int64
	CreatedAt time.Time
	Name      string
	Version   string
}

type TrackingRow struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	UserID       int64
	PackageID    int64
	UsersVersion string
}

var ErrNotFound = errors.New("not found")

//go:embed sql/get_all_packages.sql
var qGetAllPkgs string

//go:embed sql/get_package_by_name.sql
var qGetPkgByName string

//go:embed sql/get_tracking.sql
var qGetTracking string

//go:embed sql/insert_tracking.sql
var sInsertTracking string

//go:embed sql/insert_package.sql
var sInsertPackage string

func (db *Store) QueryAllPackages(ctx context.Context) ([]PackageRow, error) {
	var packages []PackageRow

	// execute query
	rows, err := db.pool.Query(ctx, qGetAllPkgs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// loop through rows and store results
	for rows.Next() {
		var pckg PackageRow
		if err := rows.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.Name, &pckg.Version); err != nil {
			return nil, err
		}
		packages = append(packages, pckg)
	}
	// check for overall query error, results may be incomplete
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return packages, nil
}

func (db *Store) QueryPackage(ctx context.Context, pckgName string) (PackageRow, error) {
	var pckg PackageRow

	// execute query
	row := db.pool.QueryRow(ctx, qGetPkgByName, pckgName)
	if err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.Name, &pckg.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pckg, ErrNotFound
		}
		return pckg, err
	}

	return pckg, nil
}

func (db *Store) QueryTracking(ctx context.Context, userID int64, pckgID int64) (TrackingRow, error) {
	var tracking TrackingRow

	// execute query
	row := db.pool.QueryRow(ctx, qGetTracking, userID, pckgID)
	if err := row.Scan(&tracking.CreatedAt, &tracking.UpdatedAt, &tracking.UserID, &tracking.PackageID, &tracking.UsersVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tracking, ErrNotFound
		}
		return tracking, err
	}

	return tracking, nil
}

func (db *Store) StorePackage(ctx context.Context, pckgName string, version string) (int64, error) {

	// execute insert
	// if there is already such package it will be updated if version changed
	// returns id of created/updated package
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertPackage, pckgName, version).Scan(&id); err != nil {
		return 0, err
	}

	return id, nil
}

func (db *Store) StoreTracking(ctx context.Context, userID int64, pckgID int64, version string) error {

	// execute insert
	// if there is already such user<->pckg tracking it will be updated if version changed
	_, err := db.pool.Exec(ctx, sInsertTracking, userID, pckgID, version)
	if err != nil {
		return err
	}

	return nil
}
