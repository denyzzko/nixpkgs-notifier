package packages

import (
	"context"
	"errors"
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
	TrackingID          string `json:"trackingID"`
	LastNotifiedVersion string `json:"lastNotifiedVersion"`
	CurrentVersion      string `json:"currentVersion"`
	UpToDate            bool   `json:"upToDate"`
}

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

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

// Checks if the user's tracked package is up to date (compares the last notified version with the current version from Nix)
func Check(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, trackingID_string string) (CheckResult, error) {
	const op = "packages.Check"

	var result CheckResult
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return result, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// convert tracking ID string to int64
	trackingID, err := strconv.ParseInt(trackingID_string, 10, 64)
	if err != nil {
		return result, appError.NewAppError(op, appError.Invalid, "invalid tracking id", err)
	}

	// fetch users tracking
	trackingRow, err := db.QueryTracking(ctx, userID, trackingID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return result, appError.NewAppError(op, appError.NotFound, "tracking not found", err)
		} else {
			return result, appError.NewAppError(op, appError.Internal, "failed to query tracking", err)
		}
	}

	// get current version from nix
	currentVersion, err := nix.GetPackageVersionByID(ctx, db, trackingRow.PackageID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return result, appError.NewAppError(op, appError.Internal, "internal error - package not found", err)
		} else if errors.Is(err, nix.ErrAttrNotFound) {
			return result, appError.NewAppError(op, appError.Invalid, "invalid request - wrong package name or branch", err)
		} else if errors.Is(err, nix.ErrEvalFailed) {
			return result, appError.NewAppError(op, appError.Upstream, "failed to get package version from Nix", err)
		}
		return result, appError.NewAppError(op, appError.Internal, "internal error", err)
	}

	// compare if version is up to date with retrieved verison from nix
	upToDate := trackingRow.LastNotifiedVersion == currentVersion

	result.TrackingID = trackingID_string
	result.LastNotifiedVersion = trackingRow.LastNotifiedVersion
	result.CurrentVersion = currentVersion
	result.UpToDate = upToDate

	return result, nil
}

// Track adds or updates package tracking for a user
// If the package doesn't exist in the database, it will be created
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
	packageRow, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
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
		packageID = packageRow.ID
	}

	// store tracking of new package for user (if already exists it will be just updated)
	err = db.StoreTracking(ctx, userID, packageID, currentVersion)
	if err != nil {
		return appError.NewAppError(op, appError.Internal, "failed to store tracking", err)
	}

	return nil
}
