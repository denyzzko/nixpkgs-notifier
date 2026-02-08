package packages

import (
	"context"
	"fmt"
	"strconv"

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

// Retrieves all packages from the database
func GetAll(ctx context.Context, db *database.Store) ([]Package, error) {
	// get all packages from database
	rows, err := db.QueryAllPackages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query packages: %w", err)
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
	var result CheckResult
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return result, fmt.Errorf("not authenticated")
	}

	// convert tracking ID string to int64
	trackingID, err := strconv.ParseInt(trackingID_string, 10, 64)
	if err != nil {
		return result, fmt.Errorf("failed to convert string to int64")
	}

	// fetch users tracking
	trackingRow, err := db.QueryTracking(ctx, userID, trackingID)
	if err != nil {
		if err == database.ErrNotFound {
			return result, fmt.Errorf("you are not tracking this package")
		} else {
			return result, fmt.Errorf("failed to query tracking: %w", err)
		}
	}

	// get current version from nix
	currentVersion, err := nix.GetPackageVersionByID(ctx, db, trackingRow.PackageID)
	if err != nil {
		return result, err
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
	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return fmt.Errorf("not authenticated")
	}

	// get current version from nix
	currentVersion, err := nix.GetPackageVersionByNameAndBranch(packageName, packageBranch)
	if err != nil {
		return fmt.Errorf("failed to get package version from Nix: %w", err)
	}

	// get package id by name and branch
	var packageID int64
	packageRow, err := db.QueryPackageByNameAndBranch(ctx, packageName, packageBranch)
	if err != nil {
		if err == database.ErrNotFound {
			// this package name and branch combination was not found -> it should be created
			//
			// TODO: what if this package name and branch combination SHOULD NOT exist ???
			//		- should not happen here as nix eval already passed before but still needs some thinking:
			//			-> when nix eval fails due to "not finding" specified package it should probably by somehow handled
			//			-> removed from database or something like this
			//			-> first of all user should not be able to send requests for nonexisting packages or branches
			packageID, err = db.StorePackage(ctx, packageName, packageBranch, currentVersion)
			if err != nil {
				return fmt.Errorf("some database error occured: %w", err)
			}
		} else {
			return fmt.Errorf("some database error occured: %w", err)
		}
	} else {
		packageID = packageRow.ID
	}

	// store tracking of new package for user (if already exists it will be just updated)
	err = db.StoreTracking(ctx, userID, packageID, currentVersion)
	if err != nil {
		return fmt.Errorf("failed to store tracking: %w", err)
	}

	return nil
}
