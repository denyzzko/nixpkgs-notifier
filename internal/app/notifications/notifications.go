package notifications

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

var ErrNotAuthenticated = errors.New("not authenticated")

// triggerUserID is 0 when triggered by the system
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

// Returns all notification records for the user
func GetDeliveryLog(ctx context.Context, db *database.Store, sm *session.SessionManager) ([]database.UserNotification, error) {
	const op = "notifications.GetDeliveryLog"

	userID := sm.GetUserID(ctx)
	if userID == 0 {
		return nil, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	logs, err := db.QueryNotificationsByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load delivery log", err)
	}

	return logs, nil
}
