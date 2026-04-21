// Package channels provides application-level logic for managing notification channels.
// It handles creating, retrieving, toggling, and deleting email and webhook channels
// for authenticated users, wrapping all database operations in typed appErrors.
package channels

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

// ChannelResult holds the resolved data for a notification channel.
type ChannelResult struct {
	ID                   int64
	Type                 string // "Email" or "Webhook"
	WebhookType          string // "generic" or "mattermost" (empty for emails)
	Address              string
	IsEnabled            bool
	DisabledByServer     bool
	MaxRetries           int
	NotifyOnManualVerify bool
	WebhookUsername      string // mattermost only
	WebhookChannel       string // mattermost only
	WebhookPriority      string // mattermost only
	WebhookRequestAck    bool   // mattermost only
}

// ChannelsSummary holds list of channels and per-type counts for the current user.
type ChannelsSummary struct {
	Channels     []ChannelResult
	WebhookCount int
	EmailCount   int
}

// ChannelTestPayload holds resolved email or webhook structs needed by dispatcher test call.
type ChannelTestPayload struct {
	Channel ChannelResult
	Email   *database.Email
	Webhook *database.Webhook
}

// GetChannels returns all channels for a user with resolved type, address and per-type counts.
func GetChannels(ctx context.Context, db *database.Store, userID int64) (ChannelsSummary, error) {
	const op = "channels.GetChannels"

	// get user channels
	rows, err := db.QueryChannelsByUserID(ctx, userID)
	if err != nil {
		return ChannelsSummary{}, appError.NewAppError(op, appError.Internal, "failed to load channels", err)
	}

	// resolve type and address for each channel, count per type
	summary := ChannelsSummary{
		Channels: make([]ChannelResult, 0, len(rows)),
	}
	for _, row := range rows {
		ch := channelResultFromRow(row)
		summary.Channels = append(summary.Channels, ch)
		if ch.Type == "Webhook" {
			summary.WebhookCount++
		} else if ch.Type == "Email" {
			summary.EmailCount++
		}
	}
	return summary, nil
}

// GetChannelByID returns a single channel by its ID.
func GetChannelByID(ctx context.Context, db *database.Store, userID int64, channelID int64) (ChannelResult, error) {
	const op = "channels.GetChannelByID"

	// get channel
	row, err := db.QueryChannelByID(ctx, channelID, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ChannelResult{}, appError.NewAppError(op, appError.NotFound, "channel not found", err)
		}
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to load channel", err)
	}

	return channelResultFromRow(row), nil
}

// channelResultFromRow maps a database.UserChannel row to a ChannelResult,
// resolving channel type (email or webhook) and its address from the fields.
func channelResultFromRow(row database.UserChannel) ChannelResult {
	ch := ChannelResult{
		ID:                   row.ID,
		IsEnabled:            row.IsEnabled,
		DisabledByServer:     row.DisabledByServer,
		NotifyOnManualVerify: row.NotifyOnManualVerify,
	}

	// resolve channel type and address (email or webhook)
	if row.Email != nil {
		ch.Type = "Email"
		ch.Address = row.Email.Address
	} else if row.Webhook != nil {
		ch.Type = "Webhook"
		ch.Address = row.Webhook.URL
		ch.WebhookType = row.Webhook.Type
		ch.WebhookUsername = row.Webhook.Username
		ch.WebhookChannel = row.Webhook.Channel
		ch.WebhookPriority = row.Webhook.Priority
		ch.WebhookRequestAck = row.Webhook.RequestAck
	}

	return ch
}

// AddChannel creates a new notification channel of the given type ("email" or "webhook") for current user.
// Returns the newly created channel ready to render.
func AddChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, chType string, address string, webhookType string, notifyOnManualVerify bool, username string, channel string, priority string, requestAck bool, maxWebhooks int, maxEmails int) (ChannelResult, error) {
	const op = "channels.Add"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return ChannelResult{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// guard: if user already has a channel with this address, return error
	existingChannels, err := db.QueryChannelsByUserID(ctx, userID)
	if err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to query channels", err)
	}
	for _, ch := range existingChannels {
		if (ch.Email != nil && ch.Email.Address == address) ||
			(ch.Webhook != nil && ch.Webhook.URL == address) {
			return ChannelResult{}, appError.NewAppError(op, appError.Conflict, "You already have a channel with this address", nil)
		}
	}

	// guard: enforce per-user webhook limit when adding a webhook channel
	if chType == "webhook" && maxWebhooks > 0 {
		webhookCount := 0
		for _, ch := range existingChannels {
			if ch.Webhook != nil {
				webhookCount++
			}
		}
		if webhookCount >= maxWebhooks {
			return ChannelResult{}, appError.NewAppError(op, appError.Conflict,
				fmt.Sprintf("Webhook limit of %d reached. Remove an existing webhook to add a new one", maxWebhooks), nil)
		}
	}

	// guard: enforce per-user email limit when adding an email channel
	if chType == "email" && maxEmails > 0 {
		emailCount := 0
		for _, ch := range existingChannels {
			if ch.Email != nil {
				emailCount++
			}
		}
		if emailCount >= maxEmails {
			return ChannelResult{}, appError.NewAppError(op, appError.Conflict,
				fmt.Sprintf("Email limit of %d reached. Remove an existing email to add a new one", maxEmails), nil)
		}
	}

	// delegate to appropriate creator based on channel type
	var id int64
	switch chType {
	case "email":
		id, err = addEmailChannel(ctx, db, userID, address, notifyOnManualVerify)
	case "webhook":
		id, err = addWebhookChannel(ctx, db, userID, address, webhookType, notifyOnManualVerify, username, channel, priority, requestAck)
	default:
		return ChannelResult{}, appError.NewAppError(op, appError.Invalid, "invalid channel type", fmt.Errorf("unknown channel type %q", chType))
	}
	if err != nil {
		return ChannelResult{}, err
	}

	// resolve the display type name
	typeName := "Email"
	if chType == "webhook" {
		typeName = "Webhook"
	}

	return ChannelResult{
		ID:                   id,
		Type:                 typeName,
		WebhookType:          webhookType,
		Address:              address,
		IsEnabled:            true,
		NotifyOnManualVerify: notifyOnManualVerify,
		WebhookUsername:      username,
		WebhookChannel:       channel,
		WebhookPriority:      priority,
		WebhookRequestAck:    requestAck,
	}, nil
}

// addEmailChannel creates a new email channel for a user.
func addEmailChannel(ctx context.Context, db *database.Store, userID int64, emailAddress string, notifyOnManualVerify bool) (int64, error) {
	const op = "channels.AddEmailChannel"

	// create email channel
	id, err := db.CreateEmailChannel(ctx, userID, emailAddress, notifyOnManualVerify)
	if err != nil {
		return 0, appError.NewAppError(op, appError.Internal, "failed to create email channel", err)
	}
	return id, nil
}

// addWebhookChannel creates a new webhook channel for a user.
func addWebhookChannel(ctx context.Context, db *database.Store, userID int64, webhookURL string, webhookType string, notifyOnManualVerify bool, username string, channel string, priority string, requestAck bool) (int64, error) {
	const op = "channels.AddWebhookChannel"

	// validate URL safety - rejects private/reserved IP addresses and non-http schemes (ftp:, mailto:, ...)
	if err := notify.ValidateWebhookURL(webhookURL); err != nil {
		return 0, appError.NewAppError(op, appError.Invalid, fmt.Sprintf("Invalid webhook URL: %v", err), err)
	}

	// create webhook channel
	id, err := db.CreateWebhookChannel(ctx, userID, webhookURL, webhookType, notifyOnManualVerify, username, channel, priority, requestAck)
	if err != nil {
		return 0, appError.NewAppError(op, appError.Internal, "failed to create webhook channel", err)
	}
	return id, nil
}

// DeleteChannel removes a channel owned by the current user.
func DeleteChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, channelIDStr string) error {
	const op = "channels.DeleteChannel"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert channel ID string to int64
	channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
	if err != nil {
		return appError.NewAppError(op, appError.Invalid, "invalid channel id", err)
	}

	// delete channel
	if err := db.DeleteChannel(ctx, channelID, userID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return appError.NewAppError(op, appError.NotFound, "channel not found", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to delete channel", err)
	}
	return nil
}

// ToggleEnabled updates is_enabled flag on a channel and returns the updated channel.
func ToggleEnabled(ctx context.Context, db *database.Store, userID int64, channelID int64, value bool) (ChannelResult, error) {
	const op = "channels.ToggleEnabled"

	// update is_enabled flag
	if err := db.UpdateChannelEnabled(ctx, channelID, userID, value); err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to update channel", err)
	}

	return GetChannelByID(ctx, db, userID, channelID)
}

// ToggleNotifyOnManualVerify updates notify_on_manual_verify flag on a channel and returns the updated channel.
func ToggleNotifyOnManualVerify(ctx context.Context, db *database.Store, userID int64, channelID int64, value bool) (ChannelResult, error) {
	const op = "channels.ToggleNotifyOnManualVerify"

	// update notify_on_manual_verify flag
	if err := db.UpdateChannelNotifyOnManualVerify(ctx, channelID, userID, value); err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to update channel", err)
	}

	return GetChannelByID(ctx, db, userID, channelID)
}

// AcknowledgeDisabled clears disabled_by_server flag for channel (channel remains disabled).
func AcknowledgeDisabled(ctx context.Context, db *database.Store, userID int64, channelID int64) (ChannelResult, error) {
	const op = "channels.AcknowledgeDisabled"

	// clear "disabled by server" flag
	if err := db.AcknowledgeChannelDisabled(ctx, channelID, userID); err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to acknowledge channel", err)
	}

	// return updated channel
	return GetChannelByID(ctx, db, userID, channelID)
}

// GetChannelTestPayload fetches channel by ID and resolves it into payload ready for dispatcher.
func GetChannelTestPayload(ctx context.Context, db *database.Store, userID int64, channelID int64) (ChannelTestPayload, error) {
	const op = "channels.GetChannelTestPayload"

	// fetch channel
	ch, err := GetChannelByID(ctx, db, userID, channelID)
	if err != nil {
		return ChannelTestPayload{}, appError.NewAppError(op, appError.NotFound, "channel not found", err)
	}

	// resolve types
	var email *database.Email
	var webhook *database.Webhook
	if ch.Type == "Email" {
		email = &database.Email{Address: ch.Address}
	} else {
		webhook = &database.Webhook{
			URL:        ch.Address,
			Type:       ch.WebhookType,
			Username:   ch.WebhookUsername,
			Channel:    ch.WebhookChannel,
			Priority:   ch.WebhookPriority,
			RequestAck: ch.WebhookRequestAck,
		}
	}

	return ChannelTestPayload{Channel: ch, Email: email, Webhook: webhook}, nil
}
