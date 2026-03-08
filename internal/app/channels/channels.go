package channels

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

type ChannelResult struct {
	ID                   int64
	Type                 string // "Email" or "Webhook"
	Address              string
	IsEnabled            bool
	NotifyOnManualVerify bool
}

// Returns all channels for a user with resolved type and address
func GetChannels(ctx context.Context, db *database.Store, sessionManager *session.SessionManager) ([]ChannelResult, error) {
	const op = "channels.GetChannels"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return nil, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get user channels
	rows, err := db.QueryChannelsByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load channels", err)
	}

	// resolve type (email/webhook) an fill array of struct
	results := make([]ChannelResult, 0, len(rows))
	for _, row := range rows {
		ch := ChannelResult{ID: row.ID, IsEnabled: row.IsEnabled}
		if row.EmailAddress != nil {
			ch.Type = "Email"
			ch.Address = *row.EmailAddress
			if row.NotifyOnManualVerify != nil {
				ch.NotifyOnManualVerify = *row.NotifyOnManualVerify
			}
		} else if row.WebhookURL != nil {
			ch.Type = "Webhook"
			ch.Address = *row.WebhookURL
			if row.NotifyOnManualVerify != nil {
				ch.NotifyOnManualVerify = *row.NotifyOnManualVerify
			}
		}
		results = append(results, ch)
	}
	return results, nil
}

// Returns a single channel by its ID
func GetChannelByID(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, channelID int64) (ChannelResult, error) {
	const op = "channels.GetChannelByID"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return ChannelResult{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get channel
	row, err := db.QueryChannelByID(ctx, channelID, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ChannelResult{}, appError.NewAppError(op, appError.NotFound, "channel not found", err)
		}
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to load channel", err)
	}

	// resolve type (email/webhook) an fill struct
	ch := ChannelResult{ID: row.ID, IsEnabled: row.IsEnabled}
	if row.EmailAddress != nil {
		ch.Type = "Email"
		ch.Address = *row.EmailAddress
		if row.NotifyOnManualVerify != nil {
			ch.NotifyOnManualVerify = *row.NotifyOnManualVerify
		}
	} else if row.WebhookURL != nil {
		ch.Type = "Webhook"
		ch.Address = *row.WebhookURL
		if row.NotifyOnManualVerify != nil {
			ch.NotifyOnManualVerify = *row.NotifyOnManualVerify
		}
	}
	return ch, nil
}

// Creates a new channel of specific type ("email" or "webhook") for the user
// Returns the created channel ready to render
func AddChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, chType string, address string, notifyOnManualVerify bool) (ChannelResult, error) {
	const op = "channels.Add"

	var id int64
	var err error

	switch chType {
	case "email":
		id, err = addEmailChannel(ctx, db, sm, address, notifyOnManualVerify)
	case "webhook":
		id, err = addWebhookChannel(ctx, db, sm, address, notifyOnManualVerify)
	default:
		return ChannelResult{}, appError.NewAppError(op, appError.Invalid, "invalid channel type", fmt.Errorf("unknown channel type %q", chType))
	}
	if err != nil {
		return ChannelResult{}, err
	}

	typeName := "Email"
	if chType == "webhook" {
		typeName = "Webhook"
	}

	return ChannelResult{
		ID:                   id,
		Type:                 typeName,
		Address:              address,
		IsEnabled:            true,
		NotifyOnManualVerify: notifyOnManualVerify,
	}, nil
}

// Creates a new email channel for the user
func addEmailChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, emailAddress string, notifyOnManualVerify bool) (int64, error) {
	const op = "channels.AddEmailChannel"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return 0, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// create email channel
	id, err := db.CreateEmailChannel(ctx, userID, emailAddress, notifyOnManualVerify)
	if err != nil {
		return 0, appError.NewAppError(op, appError.Internal, "failed to create email channel", err)
	}
	return id, nil
}

// Creates a new webhook channel for the authenticated user
func addWebhookChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, webhookURL string, notifyOnManualVerify bool) (int64, error) {
	const op = "channels.AddWebhookChannel"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return 0, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// create webhook channel
	id, err := db.CreateWebhookChannel(ctx, userID, webhookURL, notifyOnManualVerify)
	if err != nil {
		return 0, appError.NewAppError(op, appError.Internal, "failed to create webhook channel", err)
	}
	return id, nil
}

// Removes a channel owned by the user
func DeleteChannel(ctx context.Context, db *database.Store, sm *session.SessionManager, channelID_string string) error {
	const op = "channels.DeleteChannel"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert channel ID string to int64
	channelID, err := strconv.ParseInt(channelID_string, 10, 64)
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

// Updates is_enabled flag for a channel
func ToggleEnabled(ctx context.Context, db *database.Store, sm *session.SessionManager, channelID int64, value bool) (ChannelResult, error) {
	const op = "channels.ToggleEnabled"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return ChannelResult{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// update is_enabled flag
	if err := db.UpdateChannelEnabled(ctx, channelID, userID, value); err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to update channel", err)
	}

	return GetChannelByID(ctx, db, sm, channelID)
}

// Updates notify_on_manual_verify flag for a channel
func ToggleNotifyOnManualVerify(ctx context.Context, db *database.Store, sm *session.SessionManager, channelID int64, value bool) (ChannelResult, error) {
	const op = "channels.ToggleNotifyOnManualVerify"

	// get user ID
	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return ChannelResult{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// update notify_on_manual_verify flag
	if err := db.UpdateChannelNotifyOnManualVerify(ctx, channelID, value); err != nil {
		return ChannelResult{}, appError.NewAppError(op, appError.Internal, "failed to update channel", err)
	}

	return GetChannelByID(ctx, db, sm, channelID)
}
