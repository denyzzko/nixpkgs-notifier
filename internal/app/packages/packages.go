// Package packages handles all operations on tracked and watched packages.
//
// Both tracked and watched packages live in the packages table.
// Tracked packages have a trackings row and a known current_version.
// Watched packages have a watchlist row and current_version = "" (not yet in nixpkgs).
//
// Implementation is split across three files:
//   - packages.go:          shared types, CheckAll, StartBackgroundCleanup
//   - tracked_packages.go:  Track, GetTrackStatus, Untrack, Check, GetCheckStatus
//   - watched_packages.go:  Watch, Unwatch, WatchCheck, GetWatchCheckStatus
//
// Track operations use an in-memory operationResults sync.Map because a failed Track init
// rolls back both the tracking and package rows - check_state would cascade-delete with the package.
// Check and WatchCheck operations use the check_state DB table (1-hour TTL).
package packages

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

// operationResult stores outcome of a Track initialization goroutine.
// Written on completion (success or failure), read and cleared by GetTrackStatus.
// Entries not polled (e.g. user closes browser) are cleaned up by StartBackgroundCleanup.
type operationResult struct {
	failed    bool
	watchable bool // true when failure was ErrAttrNotFound - package may appear in future
	errMsg    string
	name      string
	branch    string
	createdAt time.Time
}

// operationResults stores completion signals for Track initialization goroutines.
// Key: "userID:packageID"
var operationResults sync.Map

// Result of the track polling endpoint.
// Done means goroutine finished (with success or failure).
// Failed means nix eval failed (error stored from operationResults).
type TrackStatus struct {
	Done      bool
	Failed    bool
	Watchable bool
	ErrMsg    string
	Package   database.TrackedPackage
}

// Result of Check - returned to the handler before any goroutine completes.
// Skipped means nix eval was skipped due to SkipInterval (no polling needed, render result directly).
type CheckOutcome struct {
	Package database.TrackedPackage
	Skipped bool
}

// TrackingCheckStatus is result of check polling endpoint.
// Done means goroutine finished (with success or failure).
// Failed means nix eval failed (error message is in ErrMsg).
type TrackingCheckStatus struct {
	Done           bool
	Failed         bool
	ErrMsg         string
	Package        database.TrackedPackage
	VersionChanged bool
}

// WatchlistCheckStatus is result of watchlist check polling endpoint.
// Promoted=true means package appeared in nixpkgs and a tracking row was created -
// handler will redirect to index so user sees it in their tracked list.
type WatchlistCheckStatus struct {
	Done          bool
	Failed        bool
	Promoted      bool // package appeared and tracking was created - watchlist row is gone
	StillNotFound bool // nix eval returned ErrAttrNotFound (still not in nixpkgs)
	ErrMsg        string
	Entry         database.WatchedPackage // populated for all non-promoted states
}

// AllPackagesPage holds one page of tracked and watched package records and pagination info.
type AllPackagesPage struct {
	Items       []database.PackageRow
	TotalPages  int
	CurrentPage int
}

// classifyNixError returns a user-friendly error message based on the nix error type.
func classifyNixError(err error) string {
	if errors.Is(err, nix.ErrAttrNotFound) {
		return "Invalid package name or branch"
	}
	if errors.Is(err, nix.ErrEvalFailed) {
		return "Nix evaluation failed - try again later"
	}
	return "Check failed - try again later"
}

// StartBackgroundCleanup launches a background goroutine that handles two cleanup tasks:
//  1. Removes stale operationResults entries (Track init results the user never polled).
//  2. Deletes expired check_state DB rows (1-hour TTL).
//
// Runs until ctx is cancelled (graceful shutdown).
func StartBackgroundCleanup(ctx context.Context, db *database.Store) {
	const cleanupInterval = 15 * time.Minute
	const maxAge = 15 * time.Minute

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// clean stale in-memory Track init results
				now := time.Now()
				operationResults.Range(func(key, value any) bool {
					if now.Sub(value.(operationResult).createdAt) > maxAge {
						operationResults.Delete(key)
					}
					return true
				})
				// clean expired check_state rows
				deleted, err := db.DeleteExpiredCheckStates(ctx)
				if err != nil {
					log.Printf("[ERROR] packages: delete expired check states: %v", err)
				} else if deleted > 0 {
					log.Printf("[INFO] packages: deleted %d expired check state(s)", deleted)
				}
			}
		}
	}()
}

// GetPackagesPage fetches one page of tracked and watched packages, ordered alphabetically by name.
func GetPackagesPage(ctx context.Context, db *database.Store, userID int64, page int, pageSize int) (AllPackagesPage, error) {
	const op = "packages.GetPackagesPage"

	// get total count of packages so pages can be capped in case of invalid (too high) page
	total, err := db.CountAllPackages(ctx, userID)
	if err != nil {
		return AllPackagesPage{}, appError.NewAppError(op, appError.Internal, "failed to count packages", err)
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

	// fetch requested page of packages
	items, err := db.QueryAllPackagesPaged(ctx, userID, pageSize, offset)
	if err != nil {
		return AllPackagesPage{}, appError.NewAppError(op, appError.Internal, "failed to load packages page", err)
	}

	return AllPackagesPage{
		Items:       items,
		TotalPages:  totalPages,
		CurrentPage: page,
	}, nil
}

// CheckAll enqueues a background nix eval for every tracked and watched package belonging to user.
// Clears previous check state first so old results are never mixed with fresh ones.
// Persists pending check_state rows before launching goroutines so results survive page navigation.
func CheckAll(ctx context.Context, db *database.Store, userID int64, chk *checker.Checker) error {
	const op = "packages.CheckAll"

	// clear previous results so stale results don't bleed through
	err := db.DeleteCheckStatesByUserID(ctx, userID)
	if err != nil {
		return appError.NewAppError(op, appError.Internal, "failed to clear previous check state", err)
	}

	// check all tracked packages
	tracked, err := db.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		return appError.NewAppError(op, appError.Internal, "failed to load tracked packages", err)
	}
	for _, pckg := range tracked {
		if pckg.LastNotifiedVersion == "" {
			continue // still initializing - skip
		}
		oldVer := pckg.LastNotifiedVersion
		err := db.UpsertCheckState(ctx, userID, pckg.PackageID, &oldVer)
		if err != nil {
			log.Printf("[WARN] %s: upsert check state failed (%q/%q): %v", op, pckg.Name, pckg.Branch, err)
			continue
		}
		resultCh := make(chan checker.NixResult, 1)
		skipped := chk.EnqueueHighOrSkip(checker.CheckJob{
			Name:           pckg.Name,
			Branch:         pckg.Branch,
			PackageID:      pckg.PackageID,
			CurrentVersion: pckg.CurrentVersion,
			LastCheckedAt:  pckg.LastCheckedAt,
			Result:         resultCh,
		})
		if skipped {
			// already checked recently - mark done immediately (no version change)
			err := db.UpdateCheckStateDone(ctx, userID, pckg.PackageID, nil)
			if err != nil {
				log.Printf("[WARN] %s: update check state done (skipped) (%q/%q): %v", op, pckg.Name, pckg.Branch, err)
			}
			continue
		}
		go checkPackageAsync(db, userID, pckg, oldVer, resultCh)
	}

	// check all watched packages
	watched, err := db.QueryUsersWatchedPackages(ctx, userID)
	if err != nil {
		return appError.NewAppError(op, appError.Internal, "failed to load watchlist", err)
	}
	for _, wp := range watched {
		// old_version is nil - watched packages have no version yet
		err := db.UpsertCheckState(ctx, userID, wp.PackageID, nil)
		if err != nil {
			log.Printf("[WARN] %s: upsert check state failed for watched (%q/%q): %v", op, wp.Name, wp.Branch, err)
			continue
		}
		resultCh := make(chan checker.NixResult, 1)
		chk.EnqueueHigh(checker.CheckJob{
			Name:   wp.Name,
			Branch: wp.Branch,
			Result: resultCh,
		})
		go watchCheckAsync(db, userID, wp, resultCh)
	}

	return nil
}
