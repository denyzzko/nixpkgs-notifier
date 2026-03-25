package database

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

//go:embed sql/get_packages_from_trackings_by_userID.sql
var qGetUsersTrackedPackages string

//go:embed sql/get_package_from_trackings_by_userID_and_packageID.sql
var qGetUsersTrackedPackage string

//go:embed sql/get_all_packages.sql
var qGetAllPackages string

//go:embed sql/get_package_by_NAME_BRANCH.sql
var qGetPackageByNameAndBranch string

//go:embed sql/get_package_by_ID.sql
var qGetPackage string

//go:embed sql/get_tracking_by_userID_packageID.sql
var qGetTracking string

//go:embed sql/get_account_by_ISSUER_SUBJECT.sql
var qGetAccountByIssuerSub string

//go:embed sql/get_user_by_ID.sql
var qGetUser string

//go:embed sql/get_trackings_by_packageID.sql
var qGetTrackingsByPackageID string

//go:embed sql/get_active_channels_by_userID.sql
var qGetActiveChannelsByUserID string

//go:embed sql/get_channels_by_userID.sql
var qGetChannelsByUserID string

//go:embed sql/get_channel_by_ID.sql
var qGetChannelByID string

//go:embed sql/get_all_pending_failed_notifications.sql
var qGetAllPendingFailedNotifications string

//go:embed sql/get_notifications_by_userID.sql
var qGetNotificationsByUserID string

//go:embed sql/insert_tracking.sql
var sInsertTracking string

//go:embed sql/insert_package.sql
var sInsertPackage string

//go:embed sql/insert_user.sql
var sInsertUser string

//go:embed sql/insert_account.sql
var sInsertAccount string

//go:embed sql/insert_notification.sql
var sInsertNotification string

//go:embed sql/insert_email_channel.sql
var sInsertEmailChannel string

//go:embed sql/insert_webhook_channel.sql
var sInsertWebhookChannel string

//go:embed sql/remove_tracking.sql
var dRemoveTracking string

//go:embed sql/remove_channel.sql
var dRemoveChannel string

//go:embed sql/remove_package.sql
var dRemovePackage string

//go:embed sql/update_notification_status_to_sent_by_ID.sql
var sUpdateNotificationToSent string

//go:embed sql/update_notification_status_to_failed_by_ID.sql
var sUpdateNotificationToFailed string

//go:embed sql/update_channel_is_enabled.sql
var sUpdateChannelIsEnabled string

//go:embed sql/update_notify_on_manual_verify.sql
var sUpdateNotifyOnManualVerify string

//go:embed sql/update_package_last_checked_at.sql
var sUpdatePackageLastCheckedAt string

func buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority *string, requestAck *bool) (*Email, *Webhook) {
	var email *Email
	var webhook *Webhook

	if emailAddr != nil {
		email = &Email{
			Address: *emailAddr,
		}
	}

	if webhookURL != nil {
		webhook = &Webhook{
			URL: *webhookURL,
		}
		if webhookType != nil {
			webhook.Type = *webhookType
		}
		if username != nil {
			webhook.Username = *username
		}
		if channel != nil {
			webhook.Channel = *channel
		}
		if priority != nil {
			webhook.Priority = *priority
		}
		if requestAck != nil {
			webhook.RequestAck = *requestAck
		}
	}

	return email, webhook
}

// Retrieves all packages from database that user tracks by his ID
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

// Retrieves a single package from database that user tracks identified by userID and packageID
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

// Retrieves all packages from database
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

// Retrieves package identified by id
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

// Retrieves package identified by its name and branch
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

// Updates last_checked_at timestamp for a package
// Called after every nix eval (regardless of whether the version changed)
func (db *Store) UpdatePackageLastCheckedAt(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, sUpdatePackageLastCheckedAt, packageID)
	if err != nil {
		return fmt.Errorf("database.UpdatePackageLastCheckedAt: error updating package (id=%d): %w", packageID, err)
	}
	return nil
}

// Retrieves tracking record identified by user ID and tracked package ID
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

// Retrieves all trackings rows for a specific package
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
		return 0, fmt.Errorf("database.CreateUserWithAccount: user/account id mismatch (userID=%d, linkedID=%d)", id, linkedID)
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
	row := db.pool.QueryRow(ctx, qGetUser, id)
	if err := row.Scan(&usr.ID, &usr.CreatedAt, &usr.Username, &usr.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("database.QueryUserByID: error querying user (id=%d): %w", id, err)
	}

	return usr, nil
}

// Deletes tracking identified by user ID and tracked package ID
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

// Deletes a package by ID (used to rollback a newly created package when nix eval fails)
func (db *Store) DeletePackage(ctx context.Context, packageID int64) error {
	_, err := db.pool.Exec(ctx, dRemovePackage, packageID)
	if err != nil {
		return fmt.Errorf("database.DeletePackage: error deleting package (id=%d): %w", packageID, err)
	}
	return nil
}

// Retrives all enabled (active) channels for a specific user
func (db *Store) QueryActiveChannelsByUserID(ctx context.Context, userID int64) ([]ActiveChannel, error) {
	rows, err := db.pool.Query(ctx, qGetActiveChannelsByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryActiveChannelsByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var channels []ActiveChannel
	for rows.Next() {
		var c ActiveChannel
		var emailAddr, webhookURL, webhookType, username, channel, priority *string
		var requestAck *bool

		if err := rows.Scan(&c.ChannelID, &c.UserID, &emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck, &c.NotifyOnManualVerify); err != nil {
			return nil, fmt.Errorf("database.QueryActiveChannelsByUserID: scan error: %w", err)
		}

		c.Email, c.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority, requestAck)
		channels = append(channels, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryActiveChannelsByUserID: incomplete results: %w", err)
	}
	return channels, nil
}

// Retrieves all channels for a specific user (both emails and webhooks, enabled or not)
func (db *Store) QueryChannelsByUserID(ctx context.Context, userID int64) ([]UserChannel, error) {
	rows, err := db.pool.Query(ctx, qGetChannelsByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("database.QueryChannelsByUserID: query error (userID=%d): %w", userID, err)
	}
	defer rows.Close()

	var channels []UserChannel
	for rows.Next() {
		var c UserChannel
		var emailAddr, webhookURL, webhookType, username, channel, priority *string
		var requestAck *bool

		if err := rows.Scan(&c.ID, &c.IsEnabled, &emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck, &c.NotifyOnManualVerify); err != nil {
			return nil, fmt.Errorf("database.QueryChannelsByUserID: scan error: %w", err)
		}

		c.Email, c.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority, requestAck)
		channels = append(channels, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database.QueryChannelsByUserID: incomplete results: %w", err)
	}
	return channels, nil
}

// Retrieves a single channel identified by id
func (db *Store) QueryChannelByID(ctx context.Context, channelID int64, userID int64) (UserChannel, error) {
	var c UserChannel
	var emailAddr, webhookURL, webhookType, username, channel, priority *string
	var requestAck *bool

	row := db.pool.QueryRow(ctx, qGetChannelByID, channelID, userID)
	if err := row.Scan(&c.ID, &c.IsEnabled, &emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck, &c.NotifyOnManualVerify); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserChannel{}, ErrNotFound
		}
		return UserChannel{}, fmt.Errorf("database.QueryChannelByID: error querying channel (id=%d, userID=%d): %w", channelID, userID, err)
	}

	c.Email, c.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority, requestAck)

	return c, nil
}

// 1. Updates current_version of a package identified by name and branch
// 2. Inserts pending notification for the change of this package per channel of user who tracks this package
// In one transaction for atomicity
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

// Retrieves all pending and failed (if retry count < max retries) notifications
// Includes package and channel details that are needed by the sender
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

// Marks notifiation as "failed"
// Increments attempt_count and stores the error message
func (db *Store) MarkNotificationFailed(ctx context.Context, notificationID int64, errMsg string) error {
	_, err := db.pool.Exec(ctx, sUpdateNotificationToFailed, notificationID, errMsg)
	if err != nil {
		return fmt.Errorf("database.MarkNotificationFailed: update error (id=%d): %w", notificationID, err)
	}
	return nil
}

// Creates a new email notification channel for a user
func (db *Store) CreateEmailChannel(ctx context.Context, userID int64, emailAddress string, notifyOnManualVerify bool) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertEmailChannel, userID, emailAddress, notifyOnManualVerify).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.CreateEmailChannel: error creating email channel (userID=%d): %w", userID, err)
	}
	return id, nil
}

// Creates a new webhook notification channel for a user
func (db *Store) CreateWebhookChannel(ctx context.Context, userID int64, webhookURL string, webhookType string, notifyOnManualVerify bool, username string, channel string, priority string, requestAck bool) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertWebhookChannel, userID, webhookURL, webhookType, notifyOnManualVerify, username, channel, priority, requestAck).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.CreateWebhookChannel: error creating webhook channel (userID=%d): %w", userID, err)
	}
	return id, nil
}

// Deletes user channel identified by id
func (db *Store) DeleteChannel(ctx context.Context, channelID int64, userID int64) error {
	result, err := db.pool.Exec(ctx, dRemoveChannel, channelID, userID)
	if err != nil {
		return fmt.Errorf("database.DeleteChannel: error deleting channel (channelID=%d, userID=%d): %w", channelID, userID, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Updates the is_enabled flag of a user channel
func (db *Store) UpdateChannelEnabled(ctx context.Context, channelID int64, userID int64, isEnabled bool) error {
	_, err := db.pool.Exec(ctx, sUpdateChannelIsEnabled, channelID, isEnabled, userID)
	if err != nil {
		return fmt.Errorf("database.UpdateChannelEnabled: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}

// Updates notify_on_manual_verify for a channel (email or webhook — only one row will match)
func (db *Store) UpdateChannelNotifyOnManualVerify(ctx context.Context, channelID int64, userID int64, value bool) error {
	_, err := db.pool.Exec(ctx, sUpdateNotifyOnManualVerify, channelID, value, userID)
	if err != nil {
		return fmt.Errorf("database.UpdateChannelNotifyOnManualVerify: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}

// Retrieves all notifications for a specific user
// Ordered by detected_at (descending)
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
