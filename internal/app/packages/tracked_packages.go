package packages

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

// trackInit groups package identity fields passed to the Track initialization goroutine.
// isNewPackage is true when Track created the package row -> it must be rolled back on nix failure.
type trackInit struct {
	id           int64
	name         string
	branch       string
	isNewPackage bool
}

// Retrieves all packages that user tracks by his ID.
func GetTrackedPackages(ctx context.Context, db *database.Store, userID int64) ([]database.TrackedPackage, error) {
	const op = "packages.GetTrackedPackages"

	// get all tracked packages
	trackedPackages, err := db.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load tracked packages", err)
	}

	return trackedPackages, nil
}

// Track stores a tracking record without evaluating version using nix eval for immediate return.
// Package is created with empty current_version if it doesn't exist in the system yet.
// Tracking is stored with empty last_notified_version.
// A goroutine is launched to run the nix eval and set the version.
// Polling endpoint (GET /package/status/track/{id}) checks operationResults map to detect completion.
func Track(ctx context.Context, db *database.Store, userID int64, chk *checker.Checker, packageName string, packageBranch string) (database.TrackedPackage, error) {
	const op = "packages.Track"

	var trackedPackage database.TrackedPackage

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
	go initializePackageBaseline(db, chk, userID, trackInit{
		id:           packageID,
		name:         packageName,
		branch:       packageBranch,
		isNewPackage: newPackage,
	})

	return trackedPackage, nil
}

// Runs nix eval for newly tracked package and sets the version baseline.
// Launched as a goroutine by Track.
// On failure calls rollbackTrackInit, on success calls applyTrackBaseline.
// Signals the polling endpoint via operationResults in both cases.
func initializePackageBaseline(db *database.Store, chk *checker.Checker, userID int64, pkg trackInit) {
	const op = "packages.initializePackageBaseline"
	bgCtx := context.Background()
	resultKey := fmt.Sprintf("%d:%d", userID, pkg.id)

	// get current version via checker high-priority worker pool
	resultCh := make(chan checker.NixResult, 1)
	chk.EnqueueHigh(checker.CheckJob{
		Name:      pkg.name,
		Branch:    pkg.branch,
		PackageID: pkg.id,
		Result:    resultCh,
	})
	nixResult := <-resultCh // blocks until result arrives in resultCh

	if nixResult.Err != nil {
		log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, pkg.name, pkg.branch, nixResult.Err)
		rollbackTrackInit(db, bgCtx, op, userID, pkg, nixResult.Err, resultKey)
		return
	}

	applyTrackBaseline(db, bgCtx, op, userID, pkg, nixResult.Version, resultKey)
}

// rollbackTrackInit handles the failure path of initializePackageBaseline.
// Signals polling endpoint with a failed result, then removes the tracking row
// and package row (if newly created) that Track inserted.
func rollbackTrackInit(db *database.Store, ctx context.Context, op string, userID int64, pkg trackInit, nixErr error, resultKey string) {
	// signal polling endpoint that operation failed
	watchable := errors.Is(nixErr, nix.ErrAttrNotFound)
	operationResults.Store(resultKey, operationResult{
		failed:    true,
		watchable: watchable,
		errMsg:    classifyNixError(nixErr),
		name:      pkg.name,
		branch:    pkg.branch,
		createdAt: time.Now(),
	})

	// rollback: remove tracking record that was created
	err := db.DeleteTracking(ctx, userID, pkg.id)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		log.Printf("[WARN] %s: rollback tracking delete failed (%q/%q): %v", op, pkg.name, pkg.branch, err)
	}

	// if package was newly created in the system by this Track call -> also remove it
	if pkg.isNewPackage {
		if err := db.DeletePackage(ctx, pkg.id); err != nil {
			log.Printf("[WARN] %s: rollback package delete failed (%q/%q): %v", op, pkg.name, pkg.branch, err)
		}
	}
}

// applyTrackBaseline handles the success path of initializePackageBaseline.
// Persists the resolved version, sets the last_notified_version baseline,
// and signals the polling endpoint that initialization completed.
// If user untracked while the goroutine was running, StoreTracking is skipped
// to avoid recreating a deleted tracking record.
func applyTrackBaseline(db *database.Store, ctx context.Context, op string, userID int64, pkg trackInit, currentVersion, resultKey string) {
	// update package current_version
	if _, err := db.StorePackage(ctx, pkg.name, pkg.branch, currentVersion); err != nil {
		log.Printf("[WARN] %s: update current_version failed (%q/%q): %v", op, pkg.name, pkg.branch, err)
	}

	// guard: check if user untracked the package while this goroutine was running
	// StoreTracking would just recreate a deleted tracking
	_, err := db.QueryTracking(ctx, userID, pkg.id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			log.Printf("[INFO] %s: tracking removed while initializing (%q/%q) - skipping baseline", op, pkg.name, pkg.branch)
			if dbErr := db.UpdatePackageLastCheckedAt(ctx, pkg.id); dbErr != nil {
				log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pkg.name, pkg.branch, dbErr)
			}
			operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
			return
		}
		// non-NotFound error - proceed with StoreTracking anyway but log error
		log.Printf("[WARN] %s: query tracking failed (%q/%q), proceeding anyway: %v", op, pkg.name, pkg.branch, err)
	}

	// set last_notified_version baseline
	if err := db.StoreTracking(ctx, userID, pkg.id, currentVersion); err != nil {
		log.Printf("[WARN] %s: update last_notified_version failed (%q/%q): %v", op, pkg.name, pkg.branch, err)
	}

	// update last_checked_at
	if dbErr := db.UpdatePackageLastCheckedAt(ctx, pkg.id); dbErr != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pkg.name, pkg.branch, dbErr)
	}

	// signal polling endpoint that operation completed successfully
	operationResults.Store(resultKey, operationResult{failed: false, createdAt: time.Now()})
}

// Logic for track polling endpoint.
// Called every 3s by the loading row after Track.
// Checks operationResults map (keyed by userID:packageID) to detect when the goroutine finishes.
// Returns whether tracking initialization is done, if it failed, and current package state.
func GetTrackStatus(ctx context.Context, db *database.Store, userID int64, packageIDStr string) (TrackStatus, error) {
	const op = "packages.GetTrackStatus"

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
				Done:      true,
				Failed:    true,
				Watchable: result.watchable,
				ErrMsg:    result.errMsg,
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

	// no result in operationResults yet — query DB to check if goroutine is still running
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// tracking not found - goroutine may be mid-rollback (stored result but hasn't
			// returned yet) or hasn't stored yet -> return not-done so next poll catches result
			return TrackStatus{Done: false}, nil
		}
		return TrackStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}
	return TrackStatus{Done: false, Package: pckg}, nil
}

// Untrack deletes a tracking record for a user.
func Untrack(ctx context.Context, db *database.Store, userID int64, packageIDStr string) error {
	const op = "packages.Untrack"

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// delete tracking for user
	err = db.DeleteTracking(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to delete tracking", err)
	}

	return nil
}

// Check enqueues a background nix eval for a single tracked package.
// Returns package in the state before the check and whether nix eval was skipped due to SkipInterval.
//
// If skipped: Skipped=true, no goroutine is launched, handler renders the result row directly (no polling needed).
// If not skipped: a goroutine (checkPackageAsync) runs the eval, compares versions, fires notifications if changed.
// The polling endpoint GET /package/status/check/{id} reads check_state DB to detect completion.
func Check(ctx context.Context, db *database.Store, userID int64, chk *checker.Checker, packageIDStr string) (CheckOutcome, error) {
	const op = "packages.Check"

	var empty CheckOutcome

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

	// persist pending check state so spinner row survives page navigation (1-hour TTL)
	err = db.UpsertCheckState(ctx, userID, pckg.PackageID, &prevVersion)
	if err != nil {
		log.Printf("[WARN] %s: upsert check state failed (%q/%q): %v", op, pckg.Name, pckg.Branch, err)
	}

	// nix eval enqueued - launch goroutine to process result and update check state on completion
	go checkPackageAsync(db, userID, pckg, prevVersion, resultCh)

	return CheckOutcome{Package: pckg, Skipped: false}, nil
}

// Handles the result of the nix eval enqueued by Check.
// Launched as a goroutine by Check.
//
// Compares the result with prevVersion and fires notifications if changed.
// On completion (success or failure), updates check_state DB row that Check persisted before launch.
//
// If user untracked while the goroutine was running, StoreTracking is skipped to avoid recreating it.
func checkPackageAsync(db *database.Store, userID int64, pckg database.TrackedPackage, prevVersion string, resultCh <-chan checker.NixResult) {
	const op = "packages.checkPackageAsync"
	bgCtx := context.Background()

	nixResult := <-resultCh // blocks until result arrives in resultCh

	if nixResult.Err != nil {
		log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, pckg.Name, pckg.Branch, nixResult.Err)
		dbErr := db.UpdateCheckStateFailed(bgCtx, userID, pckg.PackageID, classifyNixError(nixResult.Err))
		if dbErr != nil {
			log.Printf("[WARN] %s: update check state failed (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
		}
		return
	}

	currentVersion := nixResult.Version

	if prevVersion != currentVersion {
		// guard: check if user has untracked the package while this goroutine was running
		_, err := db.QueryTracking(bgCtx, userID, pckg.PackageID)
		if errors.Is(err, database.ErrNotFound) {
			log.Printf("[INFO] %s: tracking removed while checking (%q/%q) - skipping update", op, pckg.Name, pckg.Branch)
			dbErr := db.UpdatePackageLastCheckedAt(bgCtx, pckg.PackageID)
			if dbErr != nil {
				log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
			}
			return
		}

		// version changed - notify all users tracking this package
		notifications.CreatePendingNotifications(bgCtx, db, notifications.VersionEvent{
			PackageID:   pckg.PackageID,
			PackageName: pckg.Name,
			Branch:      pckg.Branch,
			NewVersion:  currentVersion,
		}, userID)

		// update last_notified_version for triggering user
		err = db.StoreTracking(bgCtx, userID, pckg.PackageID, currentVersion)
		if err != nil {
			log.Printf("[WARN] %s: update last_notified_version failed for %q/%q: %v", op, pckg.Name, pckg.Branch, err)
		}
	}

	// update last_checked_at
	dbErr := db.UpdatePackageLastCheckedAt(bgCtx, pckg.PackageID)
	if dbErr != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
	}

	// update check state row to done (new_version nil means no change)
	var newVer *string
	if prevVersion != currentVersion {
		newVer = &currentVersion
	}
	dbErr = db.UpdateCheckStateDone(bgCtx, userID, pckg.PackageID, newVer)
	if dbErr != nil {
		log.Printf("[WARN] %s: update check state done (%q/%q): %v", op, pckg.Name, pckg.Branch, dbErr)
	}
}

// Logic for the check polling endpoint.
// Called every 3s by the checking row after Check.
// Reads check_state DB table - the goroutine writes to it on completion.
// Returns whether the check is done and what the result was (version changed, error or no change).
func GetCheckStatus(ctx context.Context, db *database.Store, userID int64, packageIDStr string) (TrackingCheckStatus, error) {
	const op = "packages.GetCheckStatus"

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return TrackingCheckStatus{}, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// fetch users tracked package
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return TrackingCheckStatus{}, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return TrackingCheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}

	// read check state from DB
	cs, err := db.QueryCheckStateByPackage(ctx, userID, packageID)
	if err != nil {
		return TrackingCheckStatus{}, appError.NewAppError(op, appError.Internal, "failed to query check state", err)
	}
	if cs == nil {
		// no row yet - goroutine not done
		return TrackingCheckStatus{Done: false, Package: pckg}, nil
	}

	switch cs.Status {
	case "pending":
		// goroutine still running - keep polling
		return TrackingCheckStatus{Done: false, Package: pckg}, nil
	case "failed":
		// nix eval threw an error
		errMsg := ""
		if cs.ErrorMsg != nil {
			errMsg = *cs.ErrorMsg
		}
		return TrackingCheckStatus{Done: true, Failed: true, ErrMsg: errMsg, Package: pckg}, nil
	case "done":
		versionChanged := cs.NewVersion != nil
		if versionChanged {
			// patch package fields so handler can render them without an extra DB fetch
			if cs.OldVersion != nil {
				pckg.LastNotifiedVersion = *cs.OldVersion
			}
			pckg.CurrentVersion = *cs.NewVersion
		}
		return TrackingCheckStatus{Done: true, Package: pckg, VersionChanged: versionChanged}, nil
	}
	// unknown status - treat as not done
	return TrackingCheckStatus{Done: false, Package: pckg}, nil
}
