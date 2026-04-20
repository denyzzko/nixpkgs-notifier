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
	"fmt"
	"time"
)

// CreateNotificationsForVersionChange updates current_version of package and inserts
// one pending notification per channel job (atomically in one transaction).
func (db *Store) CreateNotificationsForVersionChange(ctx context.Context, packageName string, packageBranch string, newVersion string, packageID int64, jobs []ChannelNotification, detectedAt time.Time) error {
	// begin transaction
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database.CreateNotificationsForVersionChange: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// update current_version for package
	_, err = tx.Exec(ctx, sInsertPackage, packageName, packageBranch, newVersion)
	if err != nil {
		return fmt.Errorf("database.CreateNotificationsForVersionChange: update package: %w", err)
	}

	// insert pending notification (one per job)
	for _, job := range jobs {
		_, err = tx.Exec(ctx, sInsertNotification, job.Channel.ChannelID, packageID, detectedAt, job.OldVersion, newVersion)
		if err != nil {
			return fmt.Errorf("database.CreateNotificationsForVersionChange: insert notification (channelID=%d): %w", job.Channel.ChannelID, err)
		}
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database.CreateNotificationsForVersionChange: commit tx: %w", err)
	}
	return nil
}

// CreateNotificationsForFirstAppearance inserts one pending notification per channel job
// (atomically in one transaction).
// Unlike CreateNotificationsForVersionChange, it does not update the package's current_version
// because PromoteWatchlistEntries has already set it.
func (db *Store) CreateNotificationsForFirstAppearance(ctx context.Context, newVersion string, packageID int64, jobs []ChannelNotification, detectedAt time.Time) error {
	// begin transaction
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database.CreateNotificationsForFirstAppearance: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// insert pending notification (one per job)
	for _, job := range jobs {
		_, err = tx.Exec(ctx, sInsertNotification, job.Channel.ChannelID, packageID, detectedAt, job.OldVersion, newVersion)
		if err != nil {
			return fmt.Errorf("database.CreateNotificationsForFirstAppearance: insert notification (channelID=%d): %w", job.Channel.ChannelID, err)
		}
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database.CreateNotificationsForFirstAppearance: commit tx: %w", err)
	}
	return nil
}

// QueryPendingFailedNotifications retrieves all pending and failed (if retry count < max retries) notifications.
// Includes package and channel details that are needed by the sender.
func (db *Store) QueryPendingFailedNotifications(ctx context.Context, maxRetries int) ([]PendingFailedNotification, error) {
	rows, err := db.pool.Query(ctx, qGetAllPendingFailedNotifications, maxRetries)
	if err != nil {
		return nil, fmt.Errorf("database.QueryPendingNotifications: query error: %w", err)
	}
	defer rows.Close()

	var notifications []PendingFailedNotification
	for rows.Next() {
		var n PendingFailedNotification
		var emailAddr, webhookURL, webhookType, username, channel, priority *string
		var requestAck *bool

		if err := rows.Scan(
			&n.ID, &n.ChannelID, &n.PackageID, &n.PackageName, &n.PackageBranch,
			&n.OldVersion, &n.NewVersion, &n.DetectedAt, &n.AttemptCount, &n.UserID,
			&emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck,
		); err != nil {
			return nil, fmt.Errorf("database.QueryPendingNotifications: scan error: %w", err)
		}

		n.Email, n.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority, requestAck)
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryPendingNotifications: incomplete results: %w", err)
	}
	return notifications, nil
}

// 1. Marks the notification as "sent"
// 2. updates the tracking's last notified version
// In one transaction for atomicity

// MarkNotificationSent marks the notification as "sent" and updates the tracking's
// last_notified_version (atomically in one transaction).
func (db *Store) MarkNotificationSent(ctx context.Context, notificationID int64, userID int64, packageID int64, newVersion string) error {
	// begin transaction
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database.MarkNotificationSent: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// update notification status
	if _, err = tx.Exec(ctx, sUpdateNotificationToSent, notificationID); err != nil {
		return fmt.Errorf("database.MarkNotificationSent: update notification: %w", err)
	}

	// update trackings last_notified_version
	if _, err = tx.Exec(ctx, sInsertTracking, userID, packageID, newVersion); err != nil {
		return fmt.Errorf("database.MarkNotificationSent: update tracking lnv: %w", err)
	}

	// commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database.MarkNotificationSent: commit tx: %w", err)
	}
	return nil
}

// MarkNotificationFailed marks notifiation as "failed".
// Increments attempt_count and stores the error message.
func (db *Store) MarkNotificationFailed(ctx context.Context, notificationID int64, errMsg string) error {
	_, err := db.pool.Exec(ctx, sUpdateNotificationToFailed, notificationID, errMsg)
	if err != nil {
		return fmt.Errorf("database.MarkNotificationFailed: update error (id=%d): %w", notificationID, err)
	}
	return nil
}

// QueryNotificationsByUserID retrieves all notifications for a specific user.
// Ordered by detected_at (descending).
func (db *Store) QueryNotificationsByUserID(ctx context.Context, userID int64) ([]UserNotification, error) {
	rows, err := db.pool.Query(ctx, qGetNotificationsByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryNotificationsByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var notifications []UserNotification
	for rows.Next() {
		var n UserNotification
		var emailAddr, webhookURL, webhookType *string

		if err := rows.Scan(
			&n.ID, &n.DetectedAt, &n.OldVersion, &n.NewVersion,
			&n.Status, &n.AttemptCount, &n.ErrorMessage,
			&n.PackageName, &n.PackageBranch,
			&emailAddr, &webhookURL, &webhookType,
		); err != nil {
			return nil, fmt.Errorf("database.QueryNotificationsByUserID: scan error: %w", err)
		}

		n.Email, n.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, nil, nil, nil, nil)

		notifications = append(notifications, n)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryNotificationsByUserID: incomplete results: %w", err)
	}

	return notifications, nil
}

// RemoveExpiredNotifications deletes all notifications created before the given "cutoff" time.
// Returns number of rows deleted.
func (db *Store) RemoveExpiredNotifications(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := db.pool.Exec(ctx, dRemoveExpiredNotifications, cutoff)
	if err != nil {
		return 0, fmt.Errorf("database.RemoveExpiredNotifications: %w", err)
	}
	return result.RowsAffected(), nil
}

// QueryOldestNotificationCreatedAt returns the created_at timestamp of the oldest notification
// in the table (a zero time.Time if the table is empty).
func (db *Store) QueryOldestNotificationCreatedAt(ctx context.Context) (time.Time, error) {
	var oldest *time.Time
	err := db.pool.QueryRow(ctx, qGetOldestNotificationCreatedAt).Scan(&oldest)
	if err != nil {
		return time.Time{}, fmt.Errorf("database.QueryOldestNotificationCreatedAt: %w", err)
	}
	if oldest == nil {
		return time.Time{}, nil
	}
	return *oldest, nil
}
