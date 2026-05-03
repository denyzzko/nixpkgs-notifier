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

import "errors"

// ErrNotFound is returned when a queried record does not exist in database.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when an operation would violate a uniqueness constraint.
var ErrConflict = errors.New("conflict")

// ErrLastAccount is returned when an unlink would remove the user's only login method.
var ErrLastAccount = errors.New("cannot remove last account")

// buildEmailWebhook constructs Email or Webhook value from nullable SQL scan results.
// Exactly one of the two return values will be non-nil (depending on which fields are set).
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
