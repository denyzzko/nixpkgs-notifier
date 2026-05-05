package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// channelsPage renders the notification channels page with all channels current user has configured (GET /channels).
func channelsPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// get all channels with email and webhook counts
		summary, err := channels.GetChannels(ctx, db, userID)
		if err != nil {
			writeAppErr(w, "web.channelsPage", err)
			return
		}

		// get current max delivery attempt count from dispatcher config
		maxRetries := disp.Config().MaxRetries

		// render response
		chVMs := make([]pages.ChannelVM, 0, len(summary.Channels))
		for _, ch := range summary.Channels {
			chVMs = append(chVMs, channelVM(ch, maxRetries))
		}

		vm := pages.ChannelsVM{
			BaseVM:       buildBaseVM(ctx, r, db, sessionManager),
			Channels:     chVMs,
			WebhookLimit: cfg.MaxWebhooksPerUser,
			WebhookCount: summary.WebhookCount,
			EmailLimit:   cfg.MaxEmailsPerUser,
			EmailCount:   summary.EmailCount,
		}

		renderHTML(w, ctx, pages.ChannelsPage(vm))
	}
}

// addChannelForm renders the inline form for adding a new notification channel (GET /channel/add/form).
func addChannelForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		renderHTML(w, r.Context(), pages.NewChannelForm())
	}
}

// addChannelFormCancel clears inline new-channel form slot (GET /channel/add/cancel).
// Responds with empty 200 so HTMX removes the form.
func addChannelFormCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// empty response body - HTMX clears input item slot
		w.WriteHeader(http.StatusOK)
	}
}

// addChannel creates a new notification channel (email or webhook) for the current user (POST /channel/add).
// Reads type, address, notify_on_manual_verify and optional matermost webhook info from the submitted form.
// On success renders new channel row.
// On validation or application error re-renders form with an error message.
func addChannel(db *database.Store, sessionManager *session.SessionManager, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel type, adress, notify_on_manual_verify flag and mattermost webhook info value from submitted form
		rawType := r.FormValue("type")
		address := r.FormValue("address")
		notifyOnManualVerify := r.FormValue("notify_on_manual_verify") == "on"
		username := r.FormValue("username")
		channel := r.FormValue("channel")
		priority := r.FormValue("priority")
		requestAck := r.FormValue("request_ack") == "true"

		// decode type into chType + webhookType
		var chType, webhookType string
		switch rawType {
		case "email":
			chType, webhookType = "email", ""
		case "webhook_generic":
			chType, webhookType = "webhook", "generic"
		case "webhook_mattermost":
			chType, webhookType = "webhook", "mattermost"
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(rawType, address, "Invalid channel type.").Render(ctx, w)
			return
		}
		if address == "" {
			renderHTML(w, ctx, pages.NewChannelError(rawType, address, "Address is required."))
			return
		}

		// add channel
		ch, err := channels.AddChannel(ctx, db, userID,
			channels.ChannelParams{
				Type:                 chType,
				Address:              address,
				WebhookType:          webhookType,
				NotifyOnManualVerify: notifyOnManualVerify,
				Mattermost:           database.MattermostParams{Username: username, Channel: channel, Priority: priority, RequestAck: requestAck},
			},
			channels.ChannelLimits{MaxWebhooks: cfg.MaxWebhooksPerUser, MaxEmails: cfg.MaxEmailsPerUser},
		)
		if err != nil {
			renderHTML(w, ctx, pages.NewChannelError(rawType, address, appError.PublicMessage(err)))
			return

		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// deleteChannel removes a notification channel by ID (POST /channel/delete/{id}).
// Responds with empty 200 so HTMX swaps the row out of the list.
func deleteChannel(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel ID from request
		channelID := r.PathValue("id")
		if channelID == "" {
			writeGenericErr(w, "web.deleteChannel", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// delete channel
		err := channels.DeleteChannel(ctx, db, userID, channelID)
		if err != nil {
			writeAppErr(w, "web.deleteChannel", err)
			return
		}

		// empty response body - HTMX clears the item
		w.WriteHeader(http.StatusOK)
	}
}

// toggleChannelEnabled sets the enabled flag on a channel (POST /channel/toggle/{id}).
// Reads the desired boolean state from "value" form field and re-renders the channel row.
func toggleChannelEnabled(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel ID and toggle value from request
		channelID, ok := parsePathID(w, r, "web.toggleChannelEnabled", "id")
		if !ok {
			return
		}
		value := r.FormValue("value") == "true"

		// toggle enabled flag
		ch, err := channels.ToggleEnabled(ctx, db, userID, channelID, value)
		if err != nil {
			writeAppErr(w, "web.toggleChannelEnabled", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// toggleNotifyOnManualVerify sets the notify_on_manual_verify flag on a channel (POST /channel/toggle-notify/{id}).
// When enabled, a notification is sent for manual checks if version has changed.
// Reads the desired boolean state from "value" form field and re-renders the channel row.
func toggleNotifyOnManualVerify(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel ID and toggle value from request
		channelID, ok := parsePathID(w, r, "web.toggleNotifyOnManualVerify", "id")
		if !ok {
			return
		}
		value := r.FormValue("value") == "true"

		// toggle notify_on_manual_verify flag
		ch, err := channels.ToggleNotifyOnManualVerify(ctx, db, userID, channelID, value)
		if err != nil {
			writeAppErr(w, "web.toggleNotifyOnManualVerify", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// acknowledgeChannelDisabled clears "disabled by server" warning for channel (POST /channel/ack-disabled/{id}).
// Channel remains disabled, warning banner is removed and row renders normally.
func acknowledgeChannelDisabled(db *database.Store, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel ID from request
		channelID, ok := parsePathID(w, r, "web.acknowledgeChannelDisabled", "id")
		if !ok {
			return
		}

		// clear "disabled by server" flag
		ch, err := channels.AcknowledgeDisabled(ctx, db, userID, channelID)
		if err != nil {
			writeAppErr(w, "web.acknowledgeChannelDisabled", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, disp.Config().MaxRetries)))
	}
}

// testChannel sends a test message through the specified channel (POST /channel/test/{id}).
// The test is synchronous and does not create a notifications table entry.
// Re-renders the channel row with a success or failure message inline.
func testChannel(db *database.Store, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract channel ID from request
		channelID, ok := parsePathID(w, r, "web.testChannel", "id")
		if !ok {
			return
		}

		// fetch channel test payload
		payload, err := channels.GetChannelTestPayload(ctx, db, userID, channelID)
		if err != nil {
			writeAppErr(w, "web.testChannel", err)
			return
		}

		// send test message (sync, no notifications table entry)
		testErr := disp.Test(ctx, channelID, payload.Email, payload.Webhook)

		// render channel row with the result message
		if testErr != nil {
			renderHTML(w, ctx, pages.ChannelItemWithMessage(channelVM(payload.Channel, 0), "text-danger small", "Test failed: "+notify.PublicMessage(testErr)))
		} else {
			renderHTML(w, ctx, pages.ChannelItemWithMessage(channelVM(payload.Channel, 0), "text-success small", "Test message sent successfully."))
		}
	}
}
