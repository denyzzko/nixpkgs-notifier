// Package database provides the data access layer for application.
//
// It is organised in these files:
//   - database.go:              opens connection pool and runs migrations
//   - embeds.go:                embeds all SQL files into the binary at compile time
//   - models.go:                defines data types returned by queries
//   - queries_channels.go:      notification channel operations
//   - queries_config.go:        system configuration operations
//   - queries_helpers.go:       shared helpers and sentinel errors used across query files
//   - queries_notifications.go: notification creation and delivery operations
//   - queries_packages.go:      package and tracking operations
//   - queries_users.go:         user and account operations
//   - queries_watchlist.go:     watchlist operations
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// QueryActiveChannelsByUserID retrives all enabled (active) channels for a specific user.
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

// QueryChannelsByUserID retrieves all channels for a specific user (both emails and webhooks, enabled or not).
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

		if err := rows.Scan(&c.ID, &c.IsEnabled, &c.DisabledByServer, &emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck, &c.NotifyOnManualVerify); err != nil {
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

// QueryChannelByID retrieves a single channel identified by id.
func (db *Store) QueryChannelByID(ctx context.Context, channelID int64, userID int64) (UserChannel, error) {
	var c UserChannel
	var emailAddr, webhookURL, webhookType, username, channel, priority *string
	var requestAck *bool

	row := db.pool.QueryRow(ctx, qGetChannelByID, channelID, userID)
	if err := row.Scan(&c.ID, &c.IsEnabled, &c.DisabledByServer, &emailAddr, &webhookURL, &webhookType, &username, &channel, &priority, &requestAck, &c.NotifyOnManualVerify); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserChannel{}, ErrNotFound
		}
		return UserChannel{}, fmt.Errorf("database.QueryChannelByID: error querying channel (id=%d, userID=%d): %w", channelID, userID, err)
	}

	c.Email, c.Webhook = buildEmailWebhook(emailAddr, webhookURL, webhookType, username, channel, priority, requestAck)

	return c, nil
}

// CreateEmailChannel creates a new email notification channel for a user.
func (db *Store) CreateEmailChannel(ctx context.Context, userID int64, emailAddress string, notifyOnManualVerify bool) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertEmailChannel, userID, emailAddress, notifyOnManualVerify).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.CreateEmailChannel: error creating email channel (userID=%d): %w", userID, err)
	}
	return id, nil
}

// CreateWebhookChannel creates a new webhook notification channel for a user.
func (db *Store) CreateWebhookChannel(ctx context.Context, userID int64, webhookURL string, webhookType string, notifyOnManualVerify bool, username string, channel string, priority string, requestAck bool) (int64, error) {
	var id int64
	if err := db.pool.QueryRow(ctx, sInsertWebhookChannel, userID, webhookURL, webhookType, notifyOnManualVerify, username, channel, priority, requestAck).Scan(&id); err != nil {
		return 0, fmt.Errorf("database.CreateWebhookChannel: error creating webhook channel (userID=%d): %w", userID, err)
	}
	return id, nil
}

// DeleteChannel deletes user channel identified by id.
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

// UpdateChannelEnabled updates the is_enabled flag of a user channel.
func (db *Store) UpdateChannelEnabled(ctx context.Context, channelID int64, userID int64, isEnabled bool) error {
	_, err := db.pool.Exec(ctx, sUpdateChannelIsEnabled, channelID, isEnabled, userID)
	if err != nil {
		return fmt.Errorf("database.UpdateChannelEnabled: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}

// DisableChannelByServer sets is_enabled = false and disabled_by_server = true for a channel.
// Called by dispatcher when max retries are reached.
func (db *Store) DisableChannelByServer(ctx context.Context, channelID int64, userID int64) error {
	_, err := db.pool.Exec(ctx, sUpdateChannelDisableByServer, channelID, userID)
	if err != nil {
		return fmt.Errorf("database.DisableChannelByServer: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}

// AcknowledgeChannelDisabled clears disabled_by_server flag without changing is_enabled.
// Called when user clicks "Ok" on the "disabled by server" warning.
func (db *Store) AcknowledgeChannelDisabled(ctx context.Context, channelID int64, userID int64) error {
	_, err := db.pool.Exec(ctx, sUpdateChannelAckDisabled, channelID, userID)
	if err != nil {
		return fmt.Errorf("database.AcknowledgeChannelDisabled: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}

// UpdateChannelNotifyOnManualVerify updates notify_on_manual_verify for a channel (email or webhook - only one row will match).
func (db *Store) UpdateChannelNotifyOnManualVerify(ctx context.Context, channelID int64, userID int64, value bool) error {
	_, err := db.pool.Exec(ctx, sUpdateNotifyOnManualVerify, channelID, value, userID)
	if err != nil {
		return fmt.Errorf("database.UpdateChannelNotifyOnManualVerify: error updating channel (channelID=%d): %w", channelID, err)
	}
	return nil
}
