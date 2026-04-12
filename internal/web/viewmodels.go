package web

import (
	"context"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/layout"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// channelVM maps a channels.ChannelResult to the pages.ChannelVM used by channel templates.
func channelVM(ch channels.ChannelResult, maxRetries int) pages.ChannelVM {
	return pages.ChannelVM{
		ID:                   ch.ID,
		Type:                 ch.Type,
		WebhookType:          ch.WebhookType,
		Address:              ch.Address,
		IsEnabled:            ch.IsEnabled,
		DisabledByServer:     ch.DisabledByServer,
		MaxRetries:           maxRetries,
		NotifyOnManualVerify: ch.NotifyOnManualVerify,
		WebhookUsername:      ch.WebhookUsername,
		WebhookChannel:       ch.WebhookChannel,
		WebhookPriority:      ch.WebhookPriority,
		WebhookRequestAck:    ch.WebhookRequestAck,
	}
}

// trackedPackageVMFromTracked maps a database.TrackedPackage to pages.TrackedPackageVM.
func trackedPackageVMFromTracked(t database.TrackedPackage) pages.TrackedPackageVM {
	return pages.TrackedPackageVM{
		PackageID:           t.PackageID,
		Name:                t.Name,
		Branch:              t.Branch,
		LastNotifiedVersion: t.LastNotifiedVersion,
	}
}

// notificationLogVM maps a database.UserNotification to pages.NotificationLogVM.
// maxRetries is passed through so the log view can display delivery attempt progress.
func notificationLogVM(n database.UserNotification, maxRetries int) pages.NotificationLogVM {
	chType := "Webhook"
	webhookType := ""
	address := ""

	if n.Email != nil {
		chType = "Email"
		address = n.Email.Address
	} else if n.Webhook != nil {
		address = n.Webhook.URL
		webhookType = n.Webhook.Type
	}

	errMsg := ""
	if n.ErrorMessage != nil {
		errMsg = *n.ErrorMessage
	}

	return pages.NotificationLogVM{
		ID:            n.ID,
		ChannelType:   chType,
		WebhookType:   webhookType,
		Address:       address,
		DetectedAt:    n.DetectedAt,
		PackageName:   n.PackageName,
		PackageBranch: n.PackageBranch,
		OldVersion:    n.OldVersion,
		NewVersion:    n.NewVersion,
		Status:        string(n.Status),
		AttemptCount:  n.AttemptCount,
		MaxRetries:    maxRetries,
		ErrorMessage:  errMsg,
	}
}

// durationToUIValue converts duration (nanoseconds) to human-friendly value (number + unit)
// Picks the largest unit that divides evenly (falling back to seconds).
func durationToUIValue(d time.Duration) (float64, string) {
	if d == 0 {
		return 0, "seconds"
	}
	if d%time.Hour == 0 {
		return float64(d / time.Hour), "hours"
	}
	if d%time.Minute == 0 {
		return float64(d / time.Minute), "minutes"
	}
	return float64(d / time.Second), "seconds"
}

// systemConfigVM builds the view model for the admin system configuration page.
func systemConfigVM(dispCfg dispatcher.Config, checkCfg checker.Config) pages.SystemConfigVM {
	dispIntVal, dispIntUnit := durationToUIValue(dispCfg.Interval)
	checkIntVal, checkIntUnit := durationToUIValue(checkCfg.Interval)
	checkSkipIntVal, checkSkipIntUnit := durationToUIValue(checkCfg.SkipInterval)

	return pages.SystemConfigVM{
		NotificationDispatchIntervalVal:  dispIntVal,
		NotificationDispatchIntervalUnit: dispIntUnit,
		NotificationMaxRetries:           dispCfg.MaxRetries,
		NotificationDisableOnMaxRetries:  dispCfg.DisableOnMaxRetries,
		NotificationWorkerCount:          dispCfg.WorkerCount,
		PackageCheckIntervalVal:          checkIntVal,
		PackageCheckIntervalUnit:         checkIntUnit,
		PackageCheckSkipIntervalVal:      checkSkipIntVal,
		PackageCheckSkipIntervalUnit:     checkSkipIntUnit,
		PackageCheckWorkerCount:          checkCfg.WorkerCount,
	}
}

// buildBaseVM builds BaseVM passed to every page base layout.
// Returns empty BaseVM if user is not logged in.
func buildBaseVM(ctx context.Context, r *http.Request, db *database.Store, sessionManager *session.SessionManager) layout.BaseVM {
	// get user ID
	userID := sessionManager.GetUserID(r.Context())
	if userID == 0 {
		return layout.BaseVM{}
	}

	// get linked accounts for the profile menu
	acnts, _ := db.QueryAccountsByUserID(ctx, userID)
	linkedAccounts := make([]layout.LinkedAccount, 0, len(acnts))
	for _, a := range acnts {
		email := ""
		if a.Email != nil {
			email = *a.Email
		}
		linkedAccounts = append(linkedAccounts, layout.LinkedAccount{
			Provider: a.Provider,
			Email:    email,
		})
	}

	return layout.BaseVM{
		LoggedIn: true,
		IsAdmin:  sessionManager.GetUserRole(r.Context()) == "admin",
		Username: sessionManager.GetUsername(r.Context()),
		Role:     sessionManager.GetUserRole(r.Context()),
		Accounts: linkedAccounts,
	}
}
