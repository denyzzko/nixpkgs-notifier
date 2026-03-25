package web

import (
	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// channelVM maps a channels.ChannelResult to the pages.ChannelVM used by channel templates.
func channelVM(ch channels.ChannelResult) pages.ChannelVM {
	return pages.ChannelVM{
		ID:                   ch.ID,
		Type:                 ch.Type,
		WebhookType:          ch.WebhookType,
		Address:              ch.Address,
		IsEnabled:            ch.IsEnabled,
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
