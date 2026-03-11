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
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

type CheckResult struct {
	PackageID           int64
	Name                string
	Branch              string
	LastNotifiedVersion string
	CurrentVersion      string
	VersionChanged      bool
}

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

// Retrieves all packages that user tracks by his ID
func GetTrackedPackages(ctx context.Context, db *database.Store, sessionManager *session.SessionManager) ([]database.TrackedPackage, error) {
	const op = "packages.GetTracked"

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

// Track creates or updates package tracking for a user
// If the package that is to be tracked doesn't exist in the database, it is created
// Always runs nix eval (via EnqueueHigh) because cached version would set an incorrect last_notified_version baseline
func Track(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker, packageName string, packageBranch string) (database.TrackedPackage, error) {
	const op = "packages.Track"

	var trackedPackage database.TrackedPackage
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return trackedPackage, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get current version via checker high-priority worker pool
	resultCh := make(chan checker.NixResult, 1)
	chk.EnqueueHigh(checker.CheckJob{Name: packageName, Branch: packageBranch, Result: resultCh})
	nixResult := <-resultCh // blocks until result arrives in resultCh
	if nixResult.Err != nil {
		if errors.Is(nixResult.Err, nix.ErrAttrNotFound) {
			return trackedPackage, appError.NewAppError(op, appError.Invalid, "invalid package name or branch", nixResult.Err)
		} else if errors.Is(nixResult.Err, nix.ErrEvalFailed) {
			return trackedPackage, appError.NewAppError(op, appError.Upstream, "failed to get package version from Nix", nixResult.Err)
		}
		return trackedPackage, appError.NewAppError(op, appError.Internal, "internal error", nixResult.Err)
	}
	currentVersion := nixResult.Version

	// get package id by name and branch
	var packageID int64
	pckg, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// this package name and branch combination was not found -> it should be created
			packageID, err = db.StorePackage(ctx, packageName, packageBranch, currentVersion)
			if err != nil {
				return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to store package", err)
			}
		} else {
			return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to query package", err)
		}
	} else {
		packageID = pckg.ID
	}

	// update last_checked_at
	err = db.UpdatePackageLastCheckedAt(ctx, packageID)
	if err != nil {
		log.Printf("[WARN] %s: update last_checked_at failed (%q/%q): %v", op, packageName, packageBranch, err)
	}

	// store tracking of new package for user (if already exists it will be just updated)
	err = db.StoreTracking(ctx, userID, packageID, currentVersion)
	if err != nil {
		return trackedPackage, appError.NewAppError(op, appError.Internal, "failed to store tracking", err)
	}

	trackedPackage = database.TrackedPackage{
		PackageID:           packageID,
		Name:                packageName,
		Branch:              packageBranch,
		LastNotifiedVersion: currentVersion,
		CurrentVersion:      currentVersion,
	}
	return trackedPackage, nil
}

// Untreck deletes tracking for a user
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

// Checks all packages the user tracks for version updates
// All jobs are enqueued into the high-priority queue so workers process them concurrently (up to WorkerCount in parallel)
// Uses EnqueueHighOrSkip: if a package was checked within SkipInterval, its stored CurrentVersion is returned (no nix eval)
func CheckAll(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) ([]CheckResult, error) {
	const op = "packages.CheckAll"

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

	// for each tracked package check for a new version (uses just log to not fail the whole operation)
	// enqueue all jobs first to the high priority queue
	resultChans := make([]chan checker.NixResult, len(trackedPackages))
	for i, pckg := range trackedPackages {
		resultChans[i] = make(chan checker.NixResult, 1)
		chk.EnqueueHighOrSkip(checker.CheckJob{
			Name:           pckg.Name,
			Branch:         pckg.Branch,
			PackageID:      pckg.PackageID,
			CurrentVersion: pckg.CurrentVersion,
			LastCheckedAt:  pckg.LastCheckedAt,
			Result:         resultChans[i],
		})
	}

	// collect results in order
	results := make([]CheckResult, 0, len(trackedPackages))
	for i, pckg := range trackedPackages {
		nixResult := <-resultChans[i]

		var currentVersion string
		if nixResult.Err != nil {
			log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, pckg.Name, pckg.Branch, nixResult.Err)
			currentVersion = pckg.CurrentVersion
		} else {
			currentVersion = nixResult.Version
		}

		versionChanged := currentVersion != pckg.LastNotifiedVersion
		if versionChanged {
			// version change detected -> fire async notification creation for all users tracking this package
			go notifications.CreatePendingNotifications(context.Background(), db, pckg.PackageID, pckg.Name, pckg.Branch, currentVersion, userID)
		}

		results = append(results, CheckResult{
			PackageID:           pckg.PackageID,
			Name:                pckg.Name,
			Branch:              pckg.Branch,
			LastNotifiedVersion: pckg.LastNotifiedVersion,
			CurrentVersion:      currentVersion,
			VersionChanged:      versionChanged,
		})
	}

	return results, nil
}

// Checks if the user's tracked package is up to date (compares the last notified version with the current version from Nix)
func Check(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker, packageIDStr string) (CheckResult, error) {
	const op = "packages.Check"

	var result CheckResult
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return result, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageIDStr, 10, 64)
	if err != nil {
		return result, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// fetch users tracked package
	pckg, err := db.QueryUsersTrackedPackage(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return result, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		}
		return result, appError.NewAppError(op, appError.Internal, "failed to query tracked package", err)
	}

	// get current version via checker high-priority worker pool
	resultCh := make(chan checker.NixResult, 1)
	chk.EnqueueHighOrSkip(checker.CheckJob{
		Name:           pckg.Name,
		Branch:         pckg.Branch,
		PackageID:      pckg.PackageID,
		CurrentVersion: pckg.CurrentVersion,
		LastCheckedAt:  pckg.LastCheckedAt,
		Result:         resultCh,
	})
	nixResult := <-resultCh // blocks until result arrives in resultCh
	if nixResult.Err != nil {
		if errors.Is(nixResult.Err, nix.ErrAttrNotFound) {
			return result, appError.NewAppError(op, appError.Invalid, "invalid request - wrong package name or branch", nixResult.Err)
		} else if errors.Is(nixResult.Err, nix.ErrEvalFailed) {
			return result, appError.NewAppError(op, appError.Upstream, "failed to get package version from Nix", nixResult.Err)
		}
		return result, appError.NewAppError(op, appError.Internal, "internal error", nixResult.Err)
	}
	currentVersion := nixResult.Version

	result.PackageID = packageID
	result.Name = pckg.Name
	result.Branch = pckg.Branch
	result.LastNotifiedVersion = pckg.LastNotifiedVersion
	result.CurrentVersion = currentVersion
	result.VersionChanged = currentVersion != pckg.LastNotifiedVersion

	// version change detected -> fire async notification creation for all users tracking this package
	if result.VersionChanged {
		go notifications.CreatePendingNotifications(context.Background(), db, packageID, pckg.Name, pckg.Branch, currentVersion, userID)
	}

	return result, nil
}
