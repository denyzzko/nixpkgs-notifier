package packages

import (
	"context"
	"errors"
	"log"
	"strconv"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

type Package struct {
	Name           string `json:"name"`
	Branch         string `json:"branch"`
	CurrentVersion string `json:"version"`
}

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
func GetTrackedPackages(ctx context.Context, db *database.Store, userID int64) ([]database.TrackedPackage, error) {
	const op = "packages.GetTracked"

	// get all tracked packages
	trackedPackages, err := db.QueryTrackedPackagesByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load tracked packages", err)
	}

	return trackedPackages, nil
}

// Track creates or updates package tracking for a user
// If the package that is to be tracked doesn't exist in the database, it will be created
func Track(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageName string, packageBranch string) error {
	const op = "packages.Track"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get current version from nix
	currentVersion, err := nix.GetPackageVersionByNameAndBranch(ctx, packageName, packageBranch)
	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			return appError.NewAppError(op, appError.Invalid, "invalid package name or branch", err)
		} else if errors.Is(err, nix.ErrEvalFailed) {
			return appError.NewAppError(op, appError.Upstream, "failed to get package version from Nix", err)
		}
		return appError.NewAppError(op, appError.Internal, "internal error", err)
	}

	// get package id by name and branch
	var packageID int64
	pckg, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// this package name and branch combination was not found -> it should be created
			//
			// TODO: what if this package name and branch combination SHOULD NOT exist ???
			//		- should not happen here as nix eval already passed before but still needs some thinking:
			//			-> when nix eval fails due to "not finding" specified package it should probably by somehow handled
			//			-> removed from database or something like this
			//			-> first of all user should not be able to send requests for nonexisting packages or branches
			packageID, err = db.StorePackage(ctx, packageName, packageBranch, currentVersion)
			if err != nil {
				return appError.NewAppError(op, appError.Internal, "failed to store package", err)
			}
		} else {
			return appError.NewAppError(op, appError.Internal, "failed to query package", err)
		}
	} else {
		packageID = pckg.ID
	}

	// store tracking of new package for user (if already exists it will be just updated)
	err = db.StoreTracking(ctx, userID, packageID, currentVersion)
	if err != nil {
		return appError.NewAppError(op, appError.Internal, "failed to store tracking", err)
	}

	return nil
}

// Untreck deletes tracking for a user
func Untrack(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageID_string string) error {
	const op = "packages.Untrack"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageID_string, 10, 64)
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

func CheckAll(ctx context.Context, db *database.Store, sessionManager *session.SessionManager) ([]CheckResult, error) {
	const op = "packages.CheckAll"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return nil, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// get all tracked packages
	trackedPackages, err := db.QueryTrackedPackagesByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load tracked packages", err)
	}

	// for each tracked package check for a new version (uses just log to not fail the whole operation)
	results := make([]CheckResult, 0, len(trackedPackages))
	for _, pckg := range trackedPackages {
		currentVersion, err := nix.GetPackageVersionByNameAndBranch(ctx, pckg.Name, pckg.Branch)
		if err != nil {
			log.Printf("[WARN] %s: nix eval failed for %q/%q: %v", op, pckg.Name, pckg.Branch, err)
			currentVersion = pckg.LastNotifiedVersion
		}

		versionChanged := currentVersion != pckg.LastNotifiedVersion
		if versionChanged {
			packageID, err := db.StorePackage(ctx, pckg.Name, pckg.Branch, currentVersion)
			if err != nil {
				log.Printf("[WARN] %s: failed to update package version for %q/%q: %v", op, pckg.Name, pckg.Branch, err)
			} else {
				err = db.StoreTracking(ctx, userID, packageID, currentVersion)
				if err != nil {
					log.Printf("[WARN] %s: failed to update tracking for id=%d: %v", op, packageID, err)
				}
			}
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
func Check(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, packageID_string string) (CheckResult, error) {
	const op = "packages.Check"

	var result CheckResult
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return result, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert package ID string to int64
	packageID, err := strconv.ParseInt(packageID_string, 10, 64)
	if err != nil {
		return result, appError.NewAppError(op, appError.Invalid, "invalid package id", err)
	}

	// fetch users tracking
	tracking, err := db.QueryTracking(ctx, userID, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return result, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		} else {
			return result, appError.NewAppError(op, appError.Internal, "failed to query tracking", err)
		}
	}

	// fetch package from tracking
	pckg, err := db.QueryPackage(ctx, packageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return result, appError.NewAppError(op, appError.Internal, "internal error - package not found", err)
		}
		return result, appError.NewAppError(op, appError.Internal, "failed to query package", err)
	}

	// get current version of package from nix
	currentVersion, err := nix.GetPackageVersionByNameAndBranch(ctx, pckg.Name, pckg.Branch)
	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			return result, appError.NewAppError(op, appError.Invalid, "invalid request - wrong package name or branch", err)
		} else if errors.Is(err, nix.ErrEvalFailed) {
			return result, appError.NewAppError(op, appError.Upstream, "failed to get package version from Nix", err)
		}
		return result, appError.NewAppError(op, appError.Internal, "internal error", err)
	}

	result.PackageID = packageID
	result.Name = pckg.Name
	result.Branch = pckg.Branch
	result.LastNotifiedVersion = tracking.LastNotifiedVersion
	result.CurrentVersion = currentVersion
	result.VersionChanged = currentVersion != tracking.LastNotifiedVersion

	// if version changed, update both package and tracking tables
	if result.VersionChanged {
		_, err := db.StorePackage(ctx, pckg.Name, pckg.Branch, currentVersion)
		if err != nil {
			log.Printf("[WARN] %s: failed to update package version for %q/%q: %v", op, pckg.Name, pckg.Branch, err)
		} else {
			err := db.StoreTracking(ctx, userID, packageID, currentVersion)
			if err != nil {
				log.Printf("[WARN] %s: failed to update tracking for id=%d: %v", op, packageID, err)
			}
		}
	}

	return result, nil
}

/*
// Search gets version of a package from Nix
func Search(ctx context.Context, name string, branch string) (Package, error) {
	const op = "packages.Search"

	version, err := nix.GetPackageVersionByNameAndBranch(ctx, name, branch)
	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			return Package{}, appError.NewAppError(op, appError.NotFound, fmt.Sprintf("package %q not found on branch %q", name, branch), err)
		}
		return Package{}, appError.NewAppError(op, appError.Upstream, "could not reach nixpkgs, please try again", err)
	}

	//TODO: check here if user already tracks this package and return boolean

	return Package{Name: name, Branch: branch, CurrentVersion: version}, nil
}

// Retrieves all packages from the database
func GetAll(ctx context.Context, db *database.Store) ([]Package, error) {
	const op = "packages.GetAll"

	// get all packages from database
	rows, err := db.QueryAllPackages(ctx)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to query packages", err)
	}

	// convert to type Package LATER THIS WILL PROLLY NOT BE USED
	packages := make([]Package, 0, len(rows))
	for _, row := range rows {
		packages = append(packages, Package{
			Name:           row.Name,
			Branch:         row.Branch,
			CurrentVersion: row.CurrentVersion,
		})
	}

	return packages, nil
}
*/
