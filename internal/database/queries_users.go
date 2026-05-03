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

// AccountLinkParams holds all pre-resolved decisions for ApplyExistingAccountLink.
// All logic and decisions are made by the caller - this function only executes mechanically.
type AccountLinkParams struct {
	TargetUserID   int64
	SourceUserID   int64
	Issuer         string
	Subject        string
	MergeData      bool // if true: merge trackings, move channels, delete source user
	PromoteToAdmin bool // if true: promote target user to admin
}

// QueryAccountByIssuerSub retrieves account by issuer and subject.
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

// CreateUserWithAccount creates internal user with external identity (account) mapped to it.
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
		return 0, fmt.Errorf("database.CreateUserWithAccount: user/account id mismatch (userID=%d, linkedID=%d)", id, linkedID)
	}

	// commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		return 0, fmt.Errorf("database.CreateUserWithAccount: error commiting transaction: %w", err)
	}
	return id, nil
}

// QueryUserByID retrieves user by id.
func (db *Store) QueryUserByID(ctx context.Context, id int64) (User, error) {
	var usr User
	row := db.pool.QueryRow(ctx, qGetUser, id)
	if err := row.Scan(&usr.ID, &usr.CreatedAt, &usr.Username, &usr.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("database.QueryUserByID: error querying user (id=%d): %w", id, err)
	}

	return usr, nil
}

// UpdateUserUsername updates username of a user identified by user ID.
// Returns ErrConflict (sql code 23505) if the username is already taken by another user.
func (db *Store) UpdateUserUsername(ctx context.Context, userID int64, username string) error {
	result, err := db.pool.Exec(ctx, sUpdateUserUsername, userID, username)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("database.UpdateUserUsername: error updating username (userID=%d): %w", userID, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// QueryAllUsers retrieves all users from the database (ordered by created_at).
func (db *Store) QueryAllUsers(ctx context.Context) ([]User, error) {
	rows, err := db.pool.Query(ctx, qGetAllUsers)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAllUsers: query error: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.CreatedAt, &u.Username, &u.Role); err != nil {
			return nil, fmt.Errorf("database.QueryAllUsers: scan error: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryAllUsers: incomplete results: %w", err)
	}
	return users, nil
}

// UpdateUser updates the username and role of a user identified by user ID.
// Returns ErrConflict (sql code 23505) if the username is already taken by another user.
func (db *Store) UpdateUser(ctx context.Context, userID int64, username string, role string) error {
	result, err := db.pool.Exec(ctx, sUpdateUser, userID, username, role)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("database.UpdateUser: error updating user (userID=%d): %w", userID, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// QueryAccountsByUserID returns all OIDC accounts linked to a given user.
func (db *Store) QueryAccountsByUserID(ctx context.Context, userID int64) ([]Account, error) {
	rows, err := db.pool.Query(ctx, qGetAccountsByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryAccountsByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.UserID, &a.CreatedAt, &a.Provider, &a.Issuer, &a.Subject, &a.Email, &a.EmailVerified); err != nil {
			return nil, fmt.Errorf("database.QueryAccountsByUserID: scan error: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryAccountsByUserID: incomplete results: %w", err)
	}
	return accounts, nil
}

// CreateLinkedAccount inserts a new OIDC account pointing to already existing internal user.
// Unlike CreateUserWithAccount this does NOT create a new user row.
// Returns ErrConflict (sql code 23505) if the (issuer, subject) pair is already taken by some user.
func (db *Store) CreateLinkedAccount(ctx context.Context, userID int64, email *string, emailVerified bool, provider, issuer, subject string) error {
	_, err := db.pool.Exec(ctx, sInsertAccountLink, userID, email, emailVerified, provider, issuer, subject)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("database.CreateLinkedAccount: error linking account (userID=%d, issuer=%q, subject=%q): %w", userID, issuer, subject, err)
	}
	return nil
}

// ApplyExistingAccountLink executes the account merge in a single transaction.
// Steps:
//  1. Move the single account (issuer, subject) to target.
//  2. If MergeData: merge trackings, move channels, delete source user.
//  3. If PromoteToAdmin: promote target to admin.
func (db *Store) ApplyExistingAccountLink(ctx context.Context, p AccountLinkParams) error {
	// begin transaction
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("database.ApplyExistingAccountLink: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// move the single account to target
	if _, err := tx.Exec(ctx, sUpdateAccountUserByIssuerSubject, p.TargetUserID, p.Issuer, p.Subject); err != nil {
		return fmt.Errorf("database.ApplyExistingAccountLink: move account: %w", err)
	}

	if p.MergeData {
		// merge trackings: copy source into target, skip on conflict (target version wins)
		if _, err := tx.Exec(ctx, sInsertTrackingsFromUser, p.TargetUserID, p.SourceUserID); err != nil {
			return fmt.Errorf("database.ApplyExistingAccountLink: merge trackings: %w", err)
		}

		// move channels: conflicting channels are skipped
		// notifications follow automatically via FK
		if _, err := tx.Exec(ctx, sUpdateChannelsUserByUserID, p.TargetUserID, p.SourceUserID); err != nil {
			return fmt.Errorf("database.ApplyExistingAccountLink: move channels: %w", err)
		}

		// safe to delete source user: no accounts, trackings channels and notifications moved, cascade will delete the rest
		if _, err := tx.Exec(ctx, dRemoveUserByID, p.SourceUserID); err != nil {
			return fmt.Errorf("database.ApplyExistingAccountLink: delete source user (id=%d): %w", p.SourceUserID, err)
		}
	}

	if p.PromoteToAdmin {
		// promote target to admin
		if _, err := tx.Exec(ctx, sUpdateUserRoleByID, "admin", p.TargetUserID); err != nil {
			return fmt.Errorf("database.ApplyExistingAccountLink: promote target to admin: %w", err)
		}
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database.ApplyExistingAccountLink: commit: %w", err)
	}
	return nil
}

// DeleteAccountByIssuerSub removes a single OIDC account (unlink operation).
// Refuses to remove last account of user (would leave them unable to log in).
func (db *Store) DeleteAccountByIssuerSub(ctx context.Context, userID int64, issuer, subject string) error {
	// begin transaction
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("database.DeleteAccountByIssuerSub: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// count accounts this user has
	var count int
	if err := tx.QueryRow(ctx, qCountAccountsByUserID, userID).Scan(&count); err != nil {
		return fmt.Errorf("database.DeleteAccountByIssuerSub: count accounts: %w", err)
	}

	// refuse to remove last account
	if count == 1 {
		return ErrLastAccount
	}

	// delete the account
	result, err := tx.Exec(ctx, dRemoveAccountByUserIDIssuerSubject, userID, issuer, subject)
	if err != nil {
		return fmt.Errorf("database.DeleteAccountByIssuerSub: delete: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}

	// commit transaction
	return tx.Commit(ctx)
}
