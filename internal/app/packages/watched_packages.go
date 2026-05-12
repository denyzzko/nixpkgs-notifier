package packages

import (
	"context"
	"errors"
	"log"
	"strconv"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

// GetWatchedPackages returns all watchlist entries with package details for authenticated user.
func GetWatchedPackages(ctx context.Context, db *database.Store, userID int64) ([]database.WatchedPackage, error) {
	const op = "packages.GetWatchedPackages"

	// get all watchlist entries for user
	entries, err := db.QueryUsersWatchedPackages(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load watchlist", err)
	}

	return entries, nil
}

// Watch adds a package to authenticated user's watchlist.
// No nix eval - package is assumed non-existing in nixpkgs.
// Creates package row first (with current_version="") if it does not already exist,
// then creates watchlist entry pointing at it.
func Watch(ctx context.Context, db *database.Store, userID int64, packageName, packageBranch string) (database.WatchedPackage, error) {
	const op = "packages.Watch"

	// get or create package row (empty version = not yet in nixpkgs)
	pckg, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
	var packageID int64
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			packageID, err = db.StorePackage(ctx, packageName, packageBranch, "")
			if err != nil {
				return database.WatchedPackage{}, appError.NewAppError(op, appError.Internal, "failed to create package", err)
			}
		} else {
			return database.WatchedPackage{}, appError.NewAppError(op, appError.Internal, "failed to query package", err)
		}
	} else {
		packageID = pckg.ID
	}

	// add to watchlist
	entry, err := db.CreateWatchlistEntry(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			return database.WatchedPackage{}, appError.NewAppError(op, appError.Conflict, "You are already watching this package", err)
		}
		return database.WatchedPackage{}, appError.NewAppError(op, appError.Internal, "failed to add to watchlist", err)
	}

	return database.WatchedPackage{
		WatchlistID: entry.ID,
		CreatedAt:   entry.CreatedAt,
		UserID:      userID,
		PackageID:   packageID,
		Name:        packageName,
		Branch:      packageBranch,
	}, nil
}

// Unwatch removes watchlist entry for authenticated user.
// Attempts to clean up package row if nothing else references it.
func Unwatch(ctx context.Context, db *database.Store, userID int64, watchlistIDStr string) error {
	const op = "packages.Unwatch"

	// convert watchlist ID string to int64
	watchlistID, err := strconv.ParseInt(watchlistIDStr, 10, 64)
	if err != nil {
		return appError.NewAppError(op, appError.Invalid, "invalid watchlist id", err)
	}

	// delete watchlist entry
	packageID, err := db.DeleteWatchlistEntry(ctx, watchlistID, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return appError.NewAppError(op, appError.NotFound, "watchlist entry not found", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to remove from watchlist", err)
	}

	// try to delete package row
	err = db.DeleteOrphanPackage(ctx, packageID)
	if err != nil {
		log.Printf("[WARN] %s: orphan package cleanup failed (packageID=%d): %v", op, packageID, err)
	}

	return nil
}

// WatchCheck enqueues a background nix eval check for a single watched package.
// Identical to Check except EnqueueHigh is always used - SkipInterval does not apply.
// Persists a pending check_state row (old_version nil - no version yet for watched packages)
// so spinner survives page navigation.
// The polling endpoint GET /package/watch/status/check/{id} reads check_state to detect completion.
func WatchCheck(ctx context.Context, db *database.Store, userID int64, chk *checker.Checker, watchlistIDStr string) (database.WatchedPackage, error) {
	const op = "packages.WatchCheck"

	// convert watchlist ID string to int64
	watchlistID, err := strconv.ParseInt(watchlistIDStr, 10, 64)
	if err != nil {
		return database.WatchedPackage{}, appError.NewAppError(op, appError.Invalid, "invalid watchlist id", err)
	}

	// fetch watchlist entry with package details
	wp, err := db.QueryWatchlistEntry(ctx, watchlistID, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return database.WatchedPackage{}, appError.NewAppError(op, appError.NotFound, "watchlist entry not found", err)
		}
		return database.WatchedPackage{}, appError.NewAppError(op, appError.Internal, "failed to query watchlist entry", err)
	}

	// persist pending check state (old_version nil - watched packages have no version yet)
	err = db.UpsertCheckState(ctx, userID, wp.PackageID, nil)
	if err != nil {
		log.Printf("[WARN] %s: upsert check state failed (%q/%q): %v", op, wp.Name, wp.Branch, err)
	}

	// enqueue nix eval via checker high-priority worker pool (no SkipInterval for watched packages)
	resultCh := make(chan checker.NixResult, 1)
	chk.EnqueueHigh(checker.CheckJob{
		Name:   wp.Name,
		Branch: wp.Branch,
		Result: resultCh,
	})

	// nix eval enqueued - launch goroutine to process result and update check_state on completion
	go watchCheckAsync(db, userID, wp, resultCh)

	return wp, nil
}

// Handles the result of nix eval enqueued by WatchCheck.
// Launched as a goroutine by WatchCheck.
//
// On ErrAttrNotFound: updates check_state to not_found (package still not in nixpkgs).
// On any other nix error: updates check_state to failed.
// On success: calls PromoteWatchlistEntries which atomically sets current_version on the
// package row, creates tracking rows for all watchers, and removes their watchlist entries.
// After promotion check_state rows for all promoted users are deleted - polling
// endpoint detects promotion by finding watchlist row gone (ErrNotFound).
func watchCheckAsync(db *database.Store, userID int64, wp database.WatchedPackage, resultCh <-chan checker.NixResult) {
	const op = "packages.watchCheckAsync"
	bgCtx := context.Background()

	nixResult := <-resultCh // blocks until result arrives in resultCh

	if nixResult.Err != nil {
		if errors.Is(nixResult.Err, nix.ErrAttrNotFound) {
			// package still not in nixpkgs - update check state so polling endpoint can render the right row
			dbErr := db.UpdateCheckStateNotFound(bgCtx, userID, wp.PackageID)
			if dbErr != nil {
				log.Printf("[WARN] %s: update check state not_found (%q/%q): %v", op, wp.Name, wp.Branch, dbErr)
			}
			return
		}
		log.Printf("[WARN] %s: nix eval failed for watchlist entry (%q/%q): %v", op, wp.Name, wp.Branch, nixResult.Err)
		dbErr := db.UpdateCheckStateFailed(bgCtx, userID, wp.PackageID, classifyNixError(nixResult.Err))
		if dbErr != nil {
			log.Printf("[WARN] %s: update check state failed (%q/%q): %v", op, wp.Name, wp.Branch, dbErr)
		}
		return
	}

	version := nixResult.Version
	log.Printf("[INFO] %s: watchlist package appeared (%q/%q) version=%s - promoting to tracked", op, wp.Name, wp.Branch, version)

	// atomically: set current_version, delete all watchlist rows, insert tracking rows for all watchers
	userIDs, err := db.PromoteWatchlistEntries(bgCtx, wp.PackageID, version)
	if err != nil {
		log.Printf("[ERROR] %s: promote watchlist entries (%q/%q): %v", op, wp.Name, wp.Branch, err)
		dbErr := db.UpdateCheckStateFailed(bgCtx, userID, wp.PackageID, "Failed to promote - try again")
		if dbErr != nil {
			log.Printf("[WARN] %s: update check state failed after promotion error (%q/%q): %v", op, wp.Name, wp.Branch, dbErr)
		}
		return
	}

	// update last_checked_at
	dbErr := db.UpdatePackageLastCheckedAt(bgCtx, wp.PackageID)
	if dbErr != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, wp.Name, wp.Branch, dbErr)
	}

	if len(userIDs) > 0 {
		// fire notifications for all users that were watching (pass userID so notify_on_manual_verify is respected)
		notifications.CreatePendingNotificationsFirstAppearance(bgCtx, db, notifications.VersionEvent{
			PackageID:   wp.PackageID,
			PackageName: wp.Name,
			Branch:      wp.Branch,
			NewVersion:  version,
		}, userID)
	}

	// clean up check state rows - polling endpoint detects promotion by finding the watchlist row gone
	for _, uID := range userIDs {
		dbErr := db.DeleteCheckStateByPackage(bgCtx, uID, wp.PackageID)
		if dbErr != nil {
			log.Printf("[WARN] %s: delete check state after promotion (%q/%q, userID=%d): %v", op, wp.Name, wp.Branch, uID, dbErr)
		}
	}
}

// GetWatchCheckStatus is polling logic for the watchlist manual check.
// Called every 3s by the loading row rendered after POST /package/watch/check/{id}.
// Reads check_state DB table - goroutine writes to it on completion.
// Detects promotion by finding watchlist row gone (ErrNotFound) - goroutine
// deletes watchlist rows as part of PromoteWatchlistEntries. Handler redirects to index.
func GetWatchCheckStatus(ctx context.Context, db *database.Store, userID int64, watchlistIDStr string) (WatchlistCheckStatus, error) {
	const op = "packages.GetWatchCheckStatus"

	// convert watchlist ID string to int64
	watchlistID, err := strconv.ParseInt(watchlistIDStr, 10, 64)
	if err != nil {
		return WatchlistCheckStatus{}, appError.NewAppError(op, appError.Invalid, "invalid watchlist id", err)
	}

	// fetch watchlist entry with package details
	wp, err := db.QueryWatchlistEntry(ctx, watchlistID, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// watchlist row gone - package was promoted to tracked while spinner was polling
			return WatchlistCheckStatus{Done: true, Promoted: true}, nil
		}
		return WatchlistCheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query watchlist entry", err)
	}

	// read check state from DB
	cs, err := db.QueryCheckStateByPackage(ctx, userID, wp.PackageID)
	if err != nil {
		return WatchlistCheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query check state", err)
	}
	if cs == nil {
		// no row yet - goroutine not done
		return WatchlistCheckStatus{Done: false, Entry: wp}, nil
	}

	switch cs.Status {
	case "pending":
		// goroutine still running - keep polling
		return WatchlistCheckStatus{Done: false, Entry: wp}, nil
	case "not_found":
		// nix confirmed package is still not in nixpkgs
		return WatchlistCheckStatus{Done: true, StillNotFound: true, Entry: wp}, nil
	case "failed":
		// nix eval threw an error
		errMsg := ""
		if cs.ErrorMsg != nil {
			errMsg = *cs.ErrorMsg
		}
		return WatchlistCheckStatus{Done: true, Failed: true, ErrMsg: errMsg, Entry: wp}, nil
	}
	// unknown status - treat as not done
	return WatchlistCheckStatus{Done: false, Entry: wp}, nil
}
