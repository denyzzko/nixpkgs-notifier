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

//go:embed sql/user_package_by_name.sql
var qUserPkgByName string

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

func (db *Store) QueryUsersPackageByName(ctx context.Context, userID int64, pckgName string) (TrackingRow, error) {
	var tracking TrackingRow

	// execute query
	row := db.pool.QueryRow(ctx, qUserPkgByName, userID, pckgName)
	if err := row.Scan(&tracking.CreatedAt, &tracking.UpdatedAt, &tracking.UserID, &tracking.PackageID, &tracking.UsersVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tracking, ErrNotFound
		}
		return tracking, err
	}

	return tracking, nil
}
