package web

import (
	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

func channelVM(ch channels.ChannelResult) pages.ChannelVM {
	return pages.ChannelVM{
		ID:                   ch.ID,
		Type:                 ch.Type,
		Address:              ch.Address,
		IsEnabled:            ch.IsEnabled,
		NotifyOnManualVerify: ch.NotifyOnManualVerify,
	}
}

func trackedPackageVM(r packages.CheckResult) pages.TrackedPackageVM {
	return pages.TrackedPackageVM{
		PackageID:           r.PackageID,
		Name:                r.Name,
		Branch:              r.Branch,
		LastNotifiedVersion: r.LastNotifiedVersion,
		CurrentVersion:      r.CurrentVersion,
		VersionChanged:      r.VersionChanged,
		Verified:            true,
	}
}

func trackedPackageVMFromTracked(t database.TrackedPackage) pages.TrackedPackageVM {
	return pages.TrackedPackageVM{
		PackageID:           t.PackageID,
		Name:                t.Name,
		Branch:              t.Branch,
		LastNotifiedVersion: t.LastNotifiedVersion,
	}
}

func notificationLogVM(n database.UserNotification, maxRetries int) pages.NotificationLogVM {
	chType := "Webhook"
	address := ""
	if n.EmailAddress != nil {
		chType = "Email"
		address = *n.EmailAddress
	} else if n.WebhookURL != nil {
		address = *n.WebhookURL
	}

	errMsg := ""
	if n.ErrorMessage != nil {
		errMsg = *n.ErrorMessage
	}

	return pages.NotificationLogVM{
		ID:            n.ID,
		ChannelType:   chType,
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
