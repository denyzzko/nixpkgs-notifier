package packages

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

// operationResult stores the outcome of a track or check goroutine
// Written on completion (success or failure), read and cleared by GetTrackStatus/GetCheckStatus
// Entries that are not polled (e.g. user closes browser) are cleaned up by StartOperationResultCleanup
type operationResult struct {
	failed    bool
	errMsg    string
	name      string
	branch    string
	createdAt time.Time
}

// operationResults stores completion signals for track/check goroutines
// Key: "userID:packageID", Value: operationResult
var operationResults sync.Map

// Result of the track polling endpoint
// Done means goroutine finished (with success or failure)
// Failed means nix eval failed (error stored from operationResults)
type TrackStatus struct {
	Done    bool
	Failed  bool
	ErrMsg  string
	Package database.TrackedPackage
}

// Result of Check - returned to the handler before any goroutine completes
// Skipped means nix eval was skipped due to SkipInterval (no polling needed, render result directly)
type CheckOutcome struct {
	Package database.TrackedPackage
	Skipped bool
}

// Result of the check polling endpoint
// Done means goroutine finished (with success or failure)
// Failed means nix eval failed (error message is in ErrMsg)
type CheckStatus struct {
	Done           bool
	Failed         bool
	ErrMsg         string
	Package        database.TrackedPackage
	Prev           string
	VersionChanged bool
}

// Retrieves all packages that user tracks by his ID
func GetTrackedPackages(ctx context.Context, db *database.Store, sessionManager *session.SessionManager) ([]database.TrackedPackage, error) {
	const op = "packages.GetTrackedPackages"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return nil, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get all tracked packages
	trackedPackages, err := db.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load tracked packages", err)
	}

	return trackedPackages, nil
}

// Track stores a tracking record without evaluating version using nix eval for immediate return
// Package is created with empty current_version if it doesn't exist in the system yet
// Tracking is stored with empty last_notified_version
// A goroutine is launched to run the nix eval and set the version
// Polling endpoint (GET /package/status/track/{id}) checks operationResults map to detect completion
func Track(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker, packageName string, packageBranch string) (database.TrackedPackage, error) {
	const op = "packages.Track"

	var trackedPackage database.TrackedPackage
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return trackedPackage, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get or create package id by name and branch
	var packageID int64
	var newPackage bool
	pckg, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// this package name and branch combination was not found (doesn't exist) -> it should be created with empty version
			packageID, err = db.StorePackage(ctx, packageName, packageBranch, "")
			if err != nil {
				return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to store package", err)
			}
			newPackage = true
		} else {
			return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to query package", err)
		}
	} else {
		// package already exists
		packageID = pckg.ID
	}

	// guard: if user already tracks this package, return error message
	_, err = db.QueryTracking(ctx, userID, packageID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to query tracking", err)
	}
	if err == nil {
		// tracking row exists (even with empty last_notified_version = still initializing)
		return trackedPackage, appError.NewAppError(op, appError.Invalid, "You already track this package", err)
	}

	// store tracking for user
	// with empty last_notified_version as placeholder (goroutine will set the real version once nix eval completes)
	err = db.StoreTracking(ctx, userID, packageID, "")
	if err != nil {
		return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to store tracking", err)
	}

	trackedPackage = database.TrackedPackage{
		PackageID: packageID,
		Name:      packageName,
		Branch:    packageBranch,
	}

	// launch goroutine to run nix eval and set version baseline
	go initializePackageBaseline(db, chk, userID, packageID, packageName, packageBranch, newPackage)

	return trackedPackage, nil
}

// Runs nix eval for a newly tracked package and sets the version baseline
// Launched as a goroutine by Track
//
// On completion (success or failure), stores result in operationResults map so the polling endpoint can detect it
// When nix eval fails the created tracking record and package record (if it was newly created) are deleted. This is
// because they should not exist with failed nix eval. (e.g. user entered wrong package name or branch)
//
// If the user untracked while the goroutine was running and retreiving version, StoreTracking is skipped to avoid recreating it
func initializePackageBaseline(db *database.Store, chk *checker.Checker, userID int64, packageID int64, packageName string, packageBranch string, newPackage bool) {
	const op = "packages.initializePackageBaseline"
	bgCtx := context.Background()
	resultKey := fmt.Sprintf("%d:%d", userID, packageID)

	// get current version via checker high-priority worker pool
	resultCh := make(chan checker.NixResult, 1)
	chk.EnqueueHigh(checker.CheckJob{
		Name:      packageName,
		Branch:    packageBranch,
		PackageID: packageID,
		Result:    resultCh,
	})
	nixResult := <-resultCh // blocks until result arrives in resultCh

	if nixResult.Err != nil {
		log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, packageName, packageBranch, nixResult.Err)

		// rollback: remove the tracking record that was created
		if err := db.DeleteTracking(bgCtx, userID, packageID); err != nil && !errors.Is(err, database.ErrNotFound) {
			log.Printf("[WARN] %s: rollback tracking delete failed (%q/%q): %v", op, packageName, packageBranch, err)
		}
		// if the package was newly created in the system by this Track call -> also remove it
		if newPackage {
			if err := db.DeletePackage(bgCtx, packageID); err != nil {
				log.Printf("[WARN] %s: rollback package delete failed (%q/%q): %v", op, packageName, packageBranch, err)
			}
		}

		// signal polling endpoint that operation failed
		operationResults.Store(resultKey, operationResult{
			failed:    true,
			errMsg:    classifyNixError(nixResult.Err),
			name:      packageName,
			branch:    packageBranch,
			createdAt: time.Now(),
		})
		return
	}

	currentVersion := nixResult.Version

	// update package current_version
	_, err := db.StorePackage(bgCtx, packageName, packageBranch, currentVersion)
	if err != nil {
		log.Printf("[WARN] %s: update current_version failed (%q/%q): %v", op, packageName, packageBranch, err)
	}

	// guard: check if user has untracked the package while this goroutine was running
	// StoreTracking would just recreate a deleted tracking
	_, err = db.QueryTracking(bgCtx, userID, packageID)
	if errors.Is(err, database.ErrNotFound) {
		log.Printf("[INFO] %s: tracking removed while initializing (%q/%q) - skipping baseline", op, packageName, packageBranch)
		dbErr := db.UpdatePackageLastCheckedAt(bgCtx, packageID)
		if dbErr != nil {
			log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, packageName, packageBranch, dbErr)
		}
		operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
		return
	}

	// set last_notified_version baseline
	err = db.StoreTracking(bgCtx, userID, packageID, currentVersion)
	if err != nil {
		log.Printf("[WARN] %s: update last_notified_version failed (%q/%q): %v", op, packageName, packageBranch, err)
	}

	// update last_checked_at
	dbErr := db.UpdatePackageLastCheckedAt(bgCtx, packageID)
	if dbErr != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, packageName, packageBranch, dbErr)
	}

	// signal polling endpoint that operation completed successfully
	operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
}

// Logic for track polling endpoint
// Called every 3s by the loading row after Track
// Checks operationResults map (keyed by userID:packageID) to detect when the goroutine finishes
// Returns whether tracking initialization is done, if it failed, and current package state
func GetTrackStatus(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageIDStr string) (TrackStatus, error) {
	const op = "packages.GetTrackStatus"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return TrackStatus{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return TrackStatus{}, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// check operationResults map
	resultKey := fmt.Sprintf("%d:%d", userID, packageID)
	val, ok := operationResults.LoadAndDelete(resultKey)
	if ok {
		result := val.(operationResult)
		if result.failed {
			// goroutine finished with failure
			return TrackStatus{
				Done:   true,
				Failed: true,
				ErrMsg: result.errMsg,
				Package: database.TrackedPackage{
					PackageID: packageID,
					Name:      result.name,
					Branch:    result.branch,
				},
			}, nil
		}
		// goroutine finished with success - fetch package to get updated data
		pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
		if err != nil {
			return TrackStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package after completion", err)
		}
		return TrackStatus{Done: true, Failed: false, Package: pckg}, nil
	}

	// goroutine has not finished yet - fetch current package state for the loading row
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return TrackStatus{}, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return TrackStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}

	return TrackStatus{Done: false, Package: pckg}, nil
}

// Untrack deletes a tracking record for a user
func Untrack(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageIDStr string) error {
	const op = "packages.Untrack"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// delete tracking for user
	if err := db.DeleteTracking(ctx, userID, packageID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to delete tracking", err)
	}

	return nil
}

// Check enqueues a background nix eval for a single tracked package
// Returns the package state before the check and whether nix eval was skipped due to SkipInterval
//
// If skipped: Skipped=true, no goroutine is launched, handler renders the result row directly (no polling needed)
// If not skipped: a goroutine (checkPackageAsync) runs the eval, compares versions, fires notifications if changed
// The polling endpoint GET /package/status/check/{id}?prev=V checks operationResults map to detect completion
func Check(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker, packageIDStr string) (CheckOutcome, error) {
	const op = "packages.Check"

	var empty CheckOutcome

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return empty, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return empty, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// fetch users tracked package
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return empty, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return empty, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}

	// guard: empty LastNotifiedVersion means that package is not fully initialized yet - return as is so handler can show loading row
	if pckg.LastNotifiedVersion == "" {
		return CheckOutcome{Package: pckg}, nil
	}

	// save last_notified_version before goroutine runs
	prevVersion := pckg.LastNotifiedVersion

	// enqueue nix eval via checker high-priority worker pool
	// The goroutine checkPackageAsync updates last_checked_at as its final step, after all DB writes complete
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
		// nix eval was skipped (already checked recently) - no goroutine needed, handler renders result directly
		return CheckOutcome{Package: pckg, Skipped: true}, nil
	}

	// nix eval enqueued - launch goroutine to process result and signal completion
	go checkPackageAsync(db, userID, pckg, prevVersion, resultCh)

	return CheckOutcome{Package: pckg, Skipped: false}, nil
}

// Handles the result of the nix eval enqueued by Check
// Launched as a goroutine by Check
//
// Compares the result with prevVersion and fires notifications if changed
// On completion (success or failure), stores result in operationResults map so the polling endpoint can detect it
//
// If the user untracked while the goroutine was running and retreiving version, StoreTracking is skipped to avoid recreating it
func checkPackageAsync(db *database.Store, userID int64, pckg database.TrackedPackage, prevVersion string, resultCh <-chan checker.NixResult) {
	const op = "packages.checkPackageAsync"
	bgCtx := context.Background()
	resultKey := fmt.Sprintf("%d:%d", userID, pckg.PackageID)

	nixResult := <-resultCh // blocks until result arrives in resultCh

	if nixResult.Err != nil {
		log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, pckg.Name, pckg.Branch, nixResult.Err)
		// signal polling endpoint that operation failed
		operationResults.Store(resultKey, operationResult{
			failed:    true,
			errMsg:    classifyNixError(nixResult.Err),
			createdAt: time.Now(),
		})
		return
	}

	currentVersion := nixResult.Version

	if prevVersion != currentVersion {
		// guard: check if user has untracked the package while this goroutine was running
		_, err := db.QueryTracking(bgCtx, userID, pckg.PackageID)
		if errors.Is(err, database.ErrNotFound) {
			log.Printf("[INFO] %s: tracking removed while checking (%q/%q) - skipping update", op, pckg.Name, pckg.Branch)
			if dbErr := db.UpdatePackageLastCheckedAt(bgCtx, pckg.PackageID); dbErr != nil {
				log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
			}
			operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
			return
		}

		// version changed - notify all users tracking this package
		go notifications.CreatePendingNotifications(bgCtx, db, pckg.PackageID, pckg.Name, pckg.Branch, currentVersion, userID)

		// update last_notified_version for triggering user
		err = db.StoreTracking(bgCtx, userID, pckg.PackageID, currentVersion)
		if err != nil {
			log.Printf("[WARN] %s: update last_notified_version failed for %q/%q: %v", op, pckg.Name, pckg.Branch, err)
		}
	}

	// update last_checked_at
	if dbErr := db.UpdatePackageLastCheckedAt(bgCtx, pckg.PackageID); dbErr != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
	}

	// signal polling endpoint that operation completed successfully
	operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
}

// classifyNixError returns a user-friendly error message based on the nix error type
func classifyNixError(err error) string {
	if errors.Is(err, nix.ErrAttrNotFound) {
		return "Invalid package name or branch"
	}
	if errors.Is(err, nix.ErrEvalFailed) {
		return "Nix evaluation failed - try again later"
	}
	return "Check failed - try again later"
}

// Logic for the check polling endpoint
// Called every 3s by the checking row after Check
// Checks operationResults map (keyed by userID:packageID) to detect when the goroutine finishes
// Returns whether the check is done and what the result was (version changed, error, or no change)
func GetCheckStatus(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageIDStr string, prev string) (CheckStatus, error) {
	const op = "packages.GetCheckStatus"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return CheckStatus{}, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return CheckStatus{}, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// fetch users tracked package
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return CheckStatus{}, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return CheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}

	// check operationResults map for completion signal
	resultKey := fmt.Sprintf("%d:%d", userID, packageID)
	val, ok := operationResults.LoadAndDelete(resultKey)
	if !ok {
		// goroutine has not finished yet
		return CheckStatus{Done: false, Package: pckg, Prev: prev}, nil
	}

	result := val.(operationResult)
	if result.failed {
		return CheckStatus{
			Done:    true,
			Failed:  true,
			ErrMsg:  result.errMsg,
			Package: pckg,
			Prev:    prev,
		}, nil
	}

	// goroutine finished with success - re-fetch package to get updated data
	pckg, err = db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		return CheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package after completion", err)
	}

	// determine version transition using prev (snapshot from before the check)
	versionChanged := prev != "" && pckg.LastNotifiedVersion != prev

	return CheckStatus{
		Done:           true,
		Package:        pckg,
		Prev:           prev,
		VersionChanged: versionChanged,
	}, nil
}

// StartOperationResultCleanup launches a background goroutine that periodically removes
// stale entries from operationResults (e.g. entries not polled because the user closed the browser)
// Runs until ctx is cancelled (graceful shutdown)
func StartOperationResultCleanup(ctx context.Context) {
	const cleanupInterval = 60 * time.Minute
	const maxAge = 5 * time.Minute

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				operationResults.Range(func(key, value any) bool {
					if now.Sub(value.(operationResult).createdAt) > maxAge {
						operationResults.Delete(key)
					}
					return true
				})
			}
		}
	}()
}
