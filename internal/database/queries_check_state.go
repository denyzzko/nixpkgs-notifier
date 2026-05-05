package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpsertCheckState  inserts (or resets) pending check state row for (user, package).
// old_version is nil for watched packages (no version yet), non-nil for tracked packages.
func (db *Store) UpsertCheckState(ctx context.Context, userID int64, packageID int64, oldVersion *string) error {
	_, err := db.pool.Exec(ctx, sInsertCheckState, userID, packageID, oldVersion)
	if err != nil {
		return fmt.Errorf("database.UpsertCheckState : exec error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return nil
}

// QueryCheckStateByPackage returns unexpired check state row for (user, package) (or nil if absent).
func (db *Store) QueryCheckStateByPackage(ctx context.Context, userID int64, packageID int64) (*CheckState, error) {
	var cs CheckState
	row := db.pool.QueryRow(ctx, qGetCheckStateByUserPackage, userID, packageID)
	err := row.Scan(
		&cs.UserID, &cs.PackageID, &cs.Status, &cs.OldVersion, &cs.NewVersion,
		&cs.ErrorMsg, &cs.StartedAt, &cs.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("database.QueryCheckStateByPackage: scan error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return &cs, nil
}

// QueryCheckStatesByUserID returns all unexpired check state rows for a user.
// Called by indexPage to determine how to render each row (spinner, result or normal).
func (db *Store) QueryCheckStatesByUserID(ctx context.Context, userID int64) ([]CheckState, error) {
	rows, err := db.pool.Query(ctx, qGetCheckStatesByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryCheckStatesByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var states []CheckState
	for rows.Next() {
		var cs CheckState
		err := rows.Scan(
			&cs.UserID, &cs.PackageID, &cs.Status, &cs.OldVersion, &cs.NewVersion,
			&cs.ErrorMsg, &cs.StartedAt, &cs.ExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("database.QueryCheckStatesByUserID: scan error: %w", err)
		}
		states = append(states, cs)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("database.QueryCheckStatesByUserID: incomplete results: %w", err)
	}
	return states, nil
}

// UpdateCheckStateDone marks check state row as done.
// newVersion nil = no version change.
func (db *Store) UpdateCheckStateDone(ctx context.Context, userID int64, packageID int64, newVersion *string) error {
	_, err := db.pool.Exec(ctx, sUpdateCheckStateDone, userID, packageID, newVersion)
	if err != nil {
		return fmt.Errorf("database.UpdateCheckStateDone: exec error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return nil
}

// UpdateCheckStateFailed marks check state row as failed.
func (db *Store) UpdateCheckStateFailed(ctx context.Context, userID int64, packageID int64, errMsg string) error {
	_, err := db.pool.Exec(ctx, sUpdateCheckStateFailed, userID, packageID, errMsg)
	if err != nil {
		return fmt.Errorf("database.UpdateCheckStateFailed: exec error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return nil
}

// UpdateCheckStateNotFound marks check state row as not_found (package still not in nixpkgs).
// Only used for watched packages.
func (db *Store) UpdateCheckStateNotFound(ctx context.Context, userID int64, packageID int64) error {
	_, err := db.pool.Exec(ctx, sUpdateCheckStateNotFound, userID, packageID)
	if err != nil {
		return fmt.Errorf("database.UpdateCheckStateNotFound: exec error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return nil
}

// DeleteCheckStatesByUserID removes all check state rows for a user.
// Called at the start of CheckAll to clear previous states.
func (db *Store) DeleteCheckStatesByUserID(ctx context.Context, userID int64) error {
	_, err := db.pool.Exec(ctx, dRemoveCheckStatesByUserID, userID)
	if err != nil {
		return fmt.Errorf("database.DeleteCheckStatesByUserID: exec error (userID=%d): %w", userID, err)
	}
	return nil
}

// DeleteCheckStateByPackage removes single check state row for (user, package).
// Used after promotion to clean up watched package check state.
func (db *Store) DeleteCheckStateByPackage(ctx context.Context, userID int64, packageID int64) error {
	_, err := db.pool.Exec(ctx, dRemoveCheckStateByPackage, userID, packageID)
	if err != nil {
		return fmt.Errorf("database.DeleteCheckStateByPackage: exec error (userID=%d, packageID=%d): %w", userID, packageID, err)
	}
	return nil
}

// DeleteExpiredCheckStates removes all rows whose expires_at is in the past.
// Called periodically by the cleaner.
func (db *Store) DeleteExpiredCheckStates(ctx context.Context) (int64, error) {
	result, err := db.pool.Exec(ctx, dRemoveExpiredCheckStates)
	if err != nil {
		return 0, fmt.Errorf("database.DeleteExpiredCheckStates: %w", err)
	}
	return result.RowsAffected(), nil
}
