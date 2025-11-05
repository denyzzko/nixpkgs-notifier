package database

import (
	"context"
	_ "embed"
	"errors"

	"github.com/jackc/pgx/v5"
)

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

//go:embed sql/get_account_by_issuer_subject.sql
var qGetAccountByIssuerSub string

//go:embed sql/insert_user.sql
var sInsertUser string

//go:embed sql/insert_account.sql
var sInsertAccount string

type UserInfo struct {
	Email         *string
	EmailVerified bool
	Username      *string
	Role          string
	Provider      string
	Issuer        string
	Subject       string
}

func (db *Store) QueryAllPackages(ctx context.Context) ([]PackageRow, error) {
	var packages []PackageRow
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
	// if there is already such package it will be updated if version changed
	// returns id of created/updated package
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertPackage, pckgName, version).Scan(&id); err != nil {
		return 0, err
	}

	return id, nil
}

func (db *Store) StoreTracking(ctx context.Context, userID int64, pckgID int64, version string) error {
	// if there is already such user<->pckg tracking it will be updated if version changed
	_, err := db.pool.Exec(ctx, sInsertTracking, userID, pckgID, version)
	if err != nil {
		return err
	}

	return nil
}

func (db *Store) QueryAccountByIssuerSub(ctx context.Context, issuer string, subject string) (AccountRow, error) {
	var acc AccountRow
	row := db.pool.QueryRow(ctx, qGetAccountByIssuerSub, issuer, subject)
	if err := row.Scan(&acc.UserID, &acc.CreatedAt, &acc.Provider, &acc.Issuer, &acc.Subject, &acc.Email, &acc.EmailVerified); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return acc, ErrNotFound
		}
		return acc, err
	}

	return acc, nil
}

func (db *Store) CreateUserWithAccount(ctx context.Context, info UserInfo) (int64, error) {
	// begin transaction
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// create user
	var id int64
	err = tx.QueryRow(ctx, sInsertUser, info.Username, info.Role).Scan(&id)
	if err != nil {
		return 0, err
	}

	// create linking account for that user
	var linkedID int64
	err = tx.QueryRow(ctx, sInsertAccount, id, info.Email, info.EmailVerified, info.Provider, info.Issuer, info.Subject).Scan(&linkedID)
	if err != nil {
		return 0, err
	}

	if id != linkedID {
		tx.Rollback(ctx)
		return 0, errors.New("user account mismatch")
	}

	// commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		return 0, err
	}
	return id, nil
}
