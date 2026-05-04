// Package notifications handles creation of pending notification records on package version changes.
//
// When a version change is detected (by background checker or a manual user check),
// CreatePendingNotifications finds all users tracking that package
// and creates one notification job per active channel of each found user.
//
// When a watched package appears in nixpkgs for the first time, CreatePendingNotificationsFirstAppearance
// is used instead - it skips the duplicate-version check and always sets OldVersion="" so the
// notification message reads "package appeared" rather than "updated from X to Y".
//
// Each channel has a flag (NotifyOnManualVerify) that controls whether
// a user's own manual check sends a notification to that channel.
//
// All notifications and updated package version are persisted atomically
// in single database call.
package notifications

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

// LogPage holds one page of notification records and pagination info.
type LogPage struct {
	Notifications []database.UserNotification
	TotalPages    int
	CurrentPage   int
}

// GetDeliveryLogPage fetches one page of notification records for the authenticated user.
func GetDeliveryLogPage(ctx context.Context, db *database.Store, userID int64, page int, pageSize int) (LogPage, error) {
	const op = "notifications.GetDeliveryLogPage"

	// get total count of notifications so pages can be capped in case of invalid (too high) page
	total, err := db.CountNotificationsByUserID(ctx, userID)
	if err != nil {
		return LogPage{}, appError.NewAppError(op, appError.Internal, "failed to count notifications", err)
	}

	// cap page to valid range
	totalPages := 1
	if total > 0 {
		totalPages = (int(total) + pageSize - 1) / pageSize
	}
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * pageSize

	// fetch requested page of notifications
	logs, err := db.QueryNotificationsByUserIDPaged(ctx, userID, pageSize, offset)
	if err != nil {
		return LogPage{}, appError.NewAppError(op, appError.Internal, "failed to load notification log page", err)
	}

	return LogPage{
		Notifications: logs,
		TotalPages:    totalPages,
		CurrentPage:   page,
	}, nil
}

// GetDeliveryLog returns all notification records for the authenticated user.
func GetDeliveryLog(ctx context.Context, db *database.Store, userID int64) ([]database.UserNotification, error) {
	const op = "notifications.GetDeliveryLog"

	logs, err := db.QueryNotificationsByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load delivery log", err)
	}

	return logs, nil
}

// CreatePendingNotifications creates one pending notification per active channel for every user
// tracking the given package, if their last notified version is different from newVersion.
// triggerUserID is 0 for system-triggered checks. When non-zero, channels with
// NotifyOnManualVerify=false are skipped for the triggering user.
func CreatePendingNotifications(ctx context.Context, db *database.Store, packageID int64, packageName string, packageBranch string, newVersion string, triggerUserID int64) {
	detectedAt := time.Now().UTC()

	// find all user trackings for this package
	trackings, err := db.QueryTrackingsByPackageID(ctx, packageID)
	if err != nil {
		log.Printf("[ERROR] notifications: query trackings (packageID=%d): %v", packageID, err)
		return
	}

	// build jobs for each channel, that user who is tracking this package has set up
	var jobs []database.ChannelNotification
	for _, t := range trackings {
		// skip users who are already on the new version (safetycheck to not send duplicate notifications)
		if t.LastNotifiedVersion == newVersion {
			continue
		}

		// get all active channels for that user
		channels, err := db.QueryActiveChannelsByUserID(ctx, t.UserID)
		if err != nil {
			log.Printf("[WARN] notifications: query channels (userID=%d): %v", t.UserID, err)
			continue
		}

		for _, ch := range channels {
			// if this is check triggered by user (not system check) and it is his channel that is being processed
			// if that channel has notifications on manual verification turned OFF
			// 	-> skip it so notifitcation is not send to that channel
			if triggerUserID != 0 && t.UserID == triggerUserID && !ch.NotifyOnManualVerify {
				continue
			}
			jobs = append(jobs, database.ChannelNotification{
				Channel:    ch,
				OldVersion: t.LastNotifiedVersion,
			})
		}
	}

	// update package current_version and insert pending notifications in one step
	err = db.CreateNotificationsForVersionChange(ctx, packageName, packageBranch, newVersion, packageID, jobs, detectedAt)
	if err != nil {
		log.Printf("[ERROR] notifications: store version change (packageID=%d): %v", packageID, err)
		return
	}

	log.Printf("[INFO] notifications: %d pending notification(s) created for %s/%s new version: %s", len(jobs), packageName, packageBranch, newVersion)
}

// CreatePendingNotificationsFirstAppearance is like CreatePendingNotifications but used exclusively
// when a watchlist package first appears in nixpkgs.
// It skips lnv == newVersion duplicate-check because lnv was set to newVersion at
// promotion time (so tracking is immediately usable), and it always sets OldVersion=""
// so notification message correctly reads "package appeared" rather than "updated from X to Y".
func CreatePendingNotificationsFirstAppearance(ctx context.Context, db *database.Store, packageID int64, packageName string, packageBranch string, newVersion string, triggerUserID int64) {
	detectedAt := time.Now().UTC()

	// find all trackings for this package
	trackings, err := db.QueryTrackingsByPackageID(ctx, packageID)
	if err != nil {
		log.Printf("[ERROR] notifications: query trackings (packageID=%d): %v", packageID, err)
		return
	}

	// build one notification job per active channel for each tracking
	var jobs []database.ChannelNotification
	for _, t := range trackings {
		// get all active channels for this user
		channels, err := db.QueryActiveChannelsByUserID(ctx, t.UserID)
		if err != nil {
			log.Printf("[WARN] notifications: query channels (userID=%d): %v", t.UserID, err)
			continue
		}
		for _, ch := range channels {
			// skip channels where the triggering user has NotifyOnManualVerify turned off
			if triggerUserID != 0 && t.UserID == triggerUserID && !ch.NotifyOnManualVerify {
				continue
			}
			jobs = append(jobs, database.ChannelNotification{
				Channel:    ch,
				OldVersion: "", // always empty - package is appearing for the first time
			})
		}
	}

	// insert pending notifications atomically
	err = db.CreateNotificationsForFirstAppearance(ctx, newVersion, packageID, jobs, detectedAt)
	if err != nil {
		log.Printf("[ERROR] notifications: store first appearance (packageID=%d): %v", packageID, err)
		return
	}

	log.Printf("[INFO] notifications: %d first-appearance notification(s) created for %s/%s version: %s", len(jobs), packageName, packageBranch, newVersion)
}
