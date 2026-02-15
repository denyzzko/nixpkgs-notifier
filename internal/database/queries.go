package database

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

//go:embed sql/get_all_packages.sql
var qGetAllPkgs string

//go:embed sql/get_package_by_name_branch.sql
var qGetPkgByNameAndBranch string

//go:embed sql/get_package.sql
var qGetPackage string

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

//go:embed sql/get_user_by_id.sql
var qGetUserByID string

type UserInfo struct {
	Email         *string
	EmailVerified bool
	Username      *string
	Role          string
	Provider      string
	Issuer        string
	Subject       string
}

// Retrieves all packages from database
func (db *Store) QueryAllPackages(ctx context.Context) ([]Package, error) {
	var packages []Package
	rows, err := db.pool.Query(ctx, qGetAllPkgs)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllPackages: query error: %w", err)
	}
	defer rows.Close()

	// loop through rows and store results
	for rows.Next() {
		var pckg Package
		if err := rows.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion); err != nil {
			return nil, fmt.Errorf("database.QueryAllPackages: scan error: %w", err)
		}
		packages = append(packages, pckg)
	}
	// check for overall query error, results may be incomplete
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryAllPackages: incomplete results: %w", err)
	}

	return packages, nil
}

// Retrieves package identified by id
func (db *Store) QueryPackage(ctx context.Context, packageID int64) (Package, error) {
	var pckg Package
	row := db.pool.QueryRow(ctx, qGetPackage, packageID)
	if err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Package{}, ErrNotFound
		}
		return Package{}, fmt.Errorf("database.QueryPackage: error querying package (id=%d): %w", packageID, err)
	}

	return pckg, nil
}

// Retrieves package identified by its name and branch
func (db *Store) QueryPackageByNameAndBranch(ctx context.Context, name string, branch string) (Package, error) {
	var pckg Package
	row := db.pool.QueryRow(ctx, qGetPkgByNameAndBranch, name, branch)
	if err := row.Scan(&pckg.ID, &pckg.CreatedAt, &pckg.UpdatedAt, &pckg.Name, &pckg.Branch, &pckg.CurrentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Package{}, ErrNotFound
		}
		return Package{}, fmt.Errorf("database.QueryPackageByNameAndBranch: error querying package (name=%q, branch=%q): %w", name, branch, err)
	}

	return pckg, nil
}

// Retrieves tracking record for specific user
func (db *Store) QueryTracking(ctx context.Context, userID int64, trackingID int64) (Tracking, error) {
	var tracking Tracking
	row := db.pool.QueryRow(ctx, qGetTracking, userID, trackingID)
	if err := row.Scan(&tracking.CreatedAt, &tracking.UpdatedAt, &tracking.UserID, &tracking.PackageID, &tracking.LastNotifiedVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Tracking{}, ErrNotFound
		}
		return Tracking{}, fmt.Errorf("database.QueryTracking: error querying tracking (userID=%d, trackingID=%d): %w", userID, trackingID, err)
	}

	return tracking, nil
}

// Inserts or updates package in database
// Returns ID of the created/updated package (updated if version changed)
func (db *Store) StorePackage(ctx context.Context, name string, branch string, version string) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertPackage, name, branch, version).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.StorePackage: error storing package (name=%q, branch=%q): %w", name, branch, err)
	}

	return id, nil
}

// Inserts or updates tracking record (updated if version changed)
func (db *Store) StoreTracking(ctx context.Context, userID int64, packageID int64, lastNotifiedVersion string) error {
	_, err := db.pool.Exec(ctx, sInsertTracking, userID, packageID, lastNotifiedVersion)
	if err != nil {
		return fmt.Errorf("database.StoreTracking: error storing tracking (userID=%d, packageID=%d): %w", userID, packageID, err)
	}

	return nil
}

// Retrieves account by issuer and subject
func (db *Store) QueryAccountByIssuerSub(ctx context.Context, issuer string, subject string) (Account, error) {
	var acc Account
	row := db.pool.QueryRow(ctx, qGetAccountByIssuerSub, issuer, subject)
	if err := row.Scan(&acc.UserID, &acc.CreatedAt, &acc.Provider, &acc.Issuer, &acc.Subject, &acc.Email, &acc.EmailVerified); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("database.QueryAccountByIssuerSub: error queriyng account (issuer=%q, subject=%q): %w", issuer, subject, err)
	}

	return acc, nil
}

// Creates internal user with external identity (account) mapped to it
func (db *Store) CreateUserWithAccount(ctx context.Context, info UserInfo) (int64, error) {
	// begin transaction
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("database.CreateUserWithAccount: error starting transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// create user
	var id int64
	err = tx.QueryRow(ctx, sInsertUser, info.Username, info.Role).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("database.CreateUserWithAccount: error creating user: %w", err)
	}

	// create linking account for that user
	var linkedID int64
	err = tx.QueryRow(ctx, sInsertAccount, id, info.Email, info.EmailVerified, info.Provider, info.Issuer, info.Subject).Scan(&linkedID)
	if err != nil {
		return 0, fmt.Errorf("database.CreateUserWithAccount: error creating account (userID=%d): %w", id, err)
	}

	if id != linkedID {
		tx.Rollback(ctx)
		return 0, fmt.Errorf("database.CreateUserWithAccount: user/account id mismatch (userID=%d, linkedID=%d): %w", id, linkedID, err)
	}

	// commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		return 0, fmt.Errorf("database.CreateUserWithAccount: error commiting transaction: %w", err)
	}
	return id, nil
}

// Retrieves user by id
func (db *Store) QueryUserByID(ctx context.Context, id int64) (User, error) {
	var usr User
	row := db.pool.QueryRow(ctx, qGetUserByID, id)
	if err := row.Scan(&usr.ID, &usr.CreatedAt, &usr.Username, &usr.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("database.QueryUserByID: error querying user (id=%d): %w", id, err)
	}

	return usr, nil
}
