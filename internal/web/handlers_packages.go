package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// indexPage renders the home page with one page of packages the current user is tracking or watching.
func indexPage(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	const pageSize = 8
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// parse page number from query
		page := parsePageQuery(r)

		// fetch one page of tracked + watched packages
		pkgPage, err := packages.GetPackagesPage(ctx, db, userID, page, pageSize)
		if err != nil {
			writeAppErr(w, "web.indexPage", err)
			return
		}

		// build view models with check state applied so spinner/result rows render correctly on page load
		items, err := buildPackageRowVMs(ctx, db, userID, pkgPage)
		if err != nil {
			writeAppErr(w, "web.indexPage", err)
			return
		}

		// build pagination URLs
		prevURL, nextURL := buildPaginationURLs(page, pkgPage.TotalPages, "/")

		// render response
		vm := pages.IndexVM{
			BaseVM: buildBaseVM(ctx, r, db, sessionManager),
			Items:  items,
			Pagination: pages.PaginationVM{
				CurrentPage: pkgPage.CurrentPage,
				TotalPages:  pkgPage.TotalPages,
				PrevURL:     prevURL,
				NextURL:     nextURL,
			},
		}

		renderHTML(w, ctx, pages.IndexPage(vm))
	}
}

// checkTrackedPackage handles a manual version check for a tracked package (POST /package/check/{id}).
// If the package was checked recently the actual check is skipped and current row is rendered immediately with the latest stored version.
// Otherwise a background nix eval is enqueued and a loading row with a polling URL is returned so HTMX
// can poll status endpoint until eval completes.
func checkTrackedPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package ID from request
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.checkTrackedPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// enqueue async check
		outcome, err := packages.Check(ctx, db, userID, chk, packageIDStr)
		if err != nil {
			writeAppErr(w, "web.checkTrackedPackage", err)
			return
		}

		// if nix eval was skipped (checked recently), render the current row directly - no polling needed
		if outcome.Skipped {
			vm := trackedPackageVMFromTracked(outcome.Package)
			vm.CurrentVersion = outcome.Package.CurrentVersion
			vm.Verified = true
			renderHTML(w, ctx, pages.TrackedPackageItem(vm))
			return
		}

		// build polling URL
		pollingURL := fmt.Sprintf("/package/status/check/%s", packageIDStr)

		// render loading row (HTMX polls pollingURL every 3s until check is done)
		renderHTML(w, ctx, pages.TrackedPackageItemLoading(trackedPackageVMFromTracked(outcome.Package), pollingURL))
	}
}

// untrackPackage removes the tracking record for a package (POST /package/untrack/{id}).
// Responds with empty 200 so HTMX swaps the row out of the list.
func untrackPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package ID from request
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.untrackPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// delete tracking
		err := packages.Untrack(ctx, db, userID, packageIDStr)
		if err != nil {
			writeAppErr(w, "web.untrackPackage", err)
			return
		}

		// empty response body - HTMX clears the item
		w.WriteHeader(http.StatusOK)
	}
}

// trackPackageForm renders the inline form for tracking a new package (GET /package/track/form).
func trackPackageForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		renderHTML(w, r.Context(), pages.NewPackageForm())
	}
}

// trackPackageFormCancel clears the inline new-package form slot (GET /package/track/cancel).
// Responds with an empty 200 so HTMX removes the form.
func trackPackageFormCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// empty response body - HTMX clears input item slot
		w.WriteHeader(http.StatusOK)
	}
}

// trackPackage adds a new package tracking record and starts a background nix eval (POST /package/track).
// The tracking row is stored immediately. Nix eval runs in a goroutine to resolve initial version.
// Returns a loading row with a polling URL so HTMX can poll the status endpoint until eval completes.
func trackPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package name and branch from submitted form
		packageName := r.FormValue("name")
		packageBranch := r.FormValue("branch")

		if packageName == "" || packageBranch == "" {
			renderHTML(w, ctx, pages.NewPackageError(packageName, packageBranch, "Package name and branch are required."))
			return
		}

		// store tracking immediately - nix eval runs in background goroutine
		trackedPackage, err := packages.Track(ctx, db, userID, chk, packageName, packageBranch)
		if err != nil {
			renderHTML(w, ctx, pages.NewPackageError(packageName, packageBranch, appError.PublicMessage(err)))
			return
		}

		// build polling URL for track status
		pollingURL := fmt.Sprintf("/package/status/track/%d", trackedPackage.PackageID)

		// render loading row - HTMX polls pollingURL every 3s until nix eval is done
		renderHTML(w, ctx, pages.TrackedPackageItemLoading(trackedPackageVMFromTracked(trackedPackage), pollingURL))
	}
}

// packageTrackStatus is the polling endpoint for newly tracked packages.
// It is called every 3s by the loading row rendered after POST /package/track.
// Returns a spinner row (with polling) while nix eval is in progress.
// Returns final row (without polling) once operationResults map signals completion.
func packageTrackStatus(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package id
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.packageTrackStatus", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// get status of package tracking initialization
		status, err := packages.GetTrackStatus(ctx, db, userID, packageIDStr)
		if err != nil {
			writeAppErr(w, "web.packageTrackStatus", err)
			return
		}

		// build polling URL so the loading row can keep polling this endpoint
		pollingURL := fmt.Sprintf("/package/status/track/%s", packageIDStr)

		// still running - render loading row (HTMX will poll again in 3s)
		if !status.Done {
			renderHTML(w, ctx, pages.TrackedPackageItemLoading(trackedPackageVMFromTracked(status.Package), pollingURL))
			return
		}
		// nix eval failed - show error row
		if status.Failed {
			vm := trackedPackageVMFromTracked(status.Package)
			vm.ErrMsg = status.ErrMsg
			// when package doesn't exist yet, offer user to Watch it
			if status.Watchable {
				renderHTML(w, ctx, pages.TrackedPackageItemNotFound(vm))
				return
			}
			renderHTML(w, ctx, pages.TrackedPackageItemInitError(vm))
			return
		}
		// completed with success
		renderHTML(w, ctx, pages.TrackedPackageItem(trackedPackageVMFromTracked(status.Package)))
	}
}

// packageCheckStatus is the polling endpoint for manual package checks.
// It is called every 3s by the checking row rendered after POST /package/check/{id}.
// Returns a spinner row (with polling) while nix eval is in progress.
// Returns final row (without polling) once operationResults map signals completion.
func packageCheckStatus(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package id
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.packageCheckStatus", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// get status of check operation
		status, err := packages.GetCheckStatus(ctx, db, userID, packageIDStr)
		if err != nil {
			writeAppErr(w, "web.packageCheckStatus", err)
			return
		}

		// still running - render loading row (HTMX will poll again in 3s)
		if !status.Done {
			pollingURL := fmt.Sprintf("/package/status/check/%s", packageIDStr)
			renderHTML(w, ctx, pages.TrackedPackageItemLoading(trackedPackageVMFromTracked(status.Package), pollingURL))
			return
		}

		// nix eval failed - show error row with retry button
		if status.Failed {
			renderHTML(w, ctx, pages.TrackedPackageItemCheckError(trackedPackageVMFromTracked(status.Package), status.ErrMsg))
			return
		}

		// completed with success
		vm := pages.TrackedPackageVM{
			ID:                  status.Package.PackageID,
			Name:                status.Package.Name,
			Branch:              status.Package.Branch,
			LastNotifiedVersion: status.Package.LastNotifiedVersion,
			CurrentVersion:      status.Package.CurrentVersion,
			VersionChanged:      status.VersionChanged,
			Verified:            true,
		}
		renderHTML(w, ctx, pages.TrackedPackageItem(vm))
	}
}

// watchPackage adds package to the user's watchlist (POST /package/watch).
// Called after a track attempt returns error that package does not yet exist in nixpkgs.
// Returns OOB response that clears failed tracking item and renders updated watchlist section.
func watchPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract package name and branch from submitted form
		packageName := r.FormValue("name")
		packageBranch := r.FormValue("branch")
		if packageName == "" || packageBranch == "" {
			writeGenericErr(w, "web.watchPackage", "package name and branch are required", errors.New("missing form fields"), http.StatusBadRequest)
			return
		}

		// add package to watchlist
		wp, err := packages.Watch(ctx, db, userID, packageName, packageBranch)
		if err != nil {
			renderHTML(w, ctx, pages.NewPackageError(packageName, packageBranch, appError.PublicMessage(err)))
			return
		}

		// render response
		renderHTML(w, ctx, pages.WatchPackageResponse(watchedPackageVMFromWatched(wp)))
	}
}

// unwatchPackage removes package from users watchlist (POST /package/unwatch/{id}).
// Returns OOB response that removes row (and whole watchlist section if it becomes empty).
func unwatchPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract watchlist entry ID from request
		watchlistIDStr := r.PathValue("id")
		if watchlistIDStr == "" {
			writeGenericErr(w, "web.unwatchPackage", "missing watchlist id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// remove watchlist entry
		err := packages.Unwatch(ctx, db, userID, watchlistIDStr)
		if err != nil {
			writeAppErr(w, "web.unwatchPackage", err)
			return
		}

		// empty response body - HTMX clears the item
		w.WriteHeader(http.StatusOK)
	}
}

// checkWatchedPackage handles a manual nix eval check for a watched package (POST /package/watch/check/{id}).
// Unlike checkTrackedPackage, the SkipInterval check is not applied - watched packages are always checked.
// A background nix eval is enqueued and a loading row with a polling URL is returned so HTMX
// can poll the status endpoint until eval completes.
func checkWatchedPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract watchlist entry ID from request
		watchlistIDStr := r.PathValue("id")
		if watchlistIDStr == "" {
			writeGenericErr(w, "web.checkWatchedPackage", "missing watchlist id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// enqueue async check
		wp, err := packages.WatchCheck(ctx, db, userID, chk, watchlistIDStr)
		if err != nil {
			writeAppErr(w, "web.checkWatchedPackage", err)
			return
		}

		// build polling URL
		pollingURL := fmt.Sprintf("/package/watch/status/check/%s", watchlistIDStr)

		// render loading row (HTMX polls pollingURL every 3s until check is done)
		renderHTML(w, ctx, pages.WatchlistItemLoading(watchedPackageVMFromWatched(wp), pollingURL))
	}
}

// watchCheckStatus is the polling endpoint for manual watched package checks (GET /package/watch/status/check/{id}).
// Called every 3s by the loading row rendered after POST /package/watch/check/{id}.
// It is similar to packageCheckStatus bu no prev query param (there is no prior version), extra StillNotFound branch (package does not exist yet),
// and Promoted branch instead of success branch (first appearance, not version change).
func watchCheckStatus(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract watchlist entry ID from request
		watchlistIDStr := r.PathValue("id")
		if watchlistIDStr == "" {
			writeGenericErr(w, "web.watchCheckStatus", "missing watchlist id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// get status of check operation
		status, err := packages.GetWatchCheckStatus(ctx, db, userID, watchlistIDStr)
		if err != nil {
			writeAppErr(w, "web.watchCheckStatus", err)
			return
		}

		// build polling URL
		pollingURL := fmt.Sprintf("/package/watch/status/check/%s", watchlistIDStr)

		// still running - render loading row (HTMX will poll again in 3s)
		if !status.Done {
			renderHTML(w, ctx, pages.WatchlistItemLoading(watchedPackageVMFromWatched(status.Entry), pollingURL))
			return
		}

		// nix eval failed - show error row with retry button
		if status.Failed {
			vm := watchedPackageVMFromWatched(status.Entry)
			vm.ErrMsg = status.ErrMsg
			renderHTML(w, ctx, pages.WatchlistItemError(vm))
			return
		}

		// package still not in nixpkgs - render normal row with "still not there" badge
		if status.StillNotFound {
			vm := watchedPackageVMFromWatched(status.Entry)
			vm.CheckedNotFound = true
			renderHTML(w, ctx, pages.WatchlistItem(vm))
			return
		}

		// package appeared (promoted) - watchlist row is gone and a tracking row now exists.
		// Redirect to index page so user sees package in their tracked list.
		if status.Promoted {
			w.Header().Set("HX-Redirect", "/")
			w.WriteHeader(http.StatusOK)
			return
		}
	}
}

// checkAllPackages enqueues nix eval for every tracked and watched package (POST /packages/check-all).
// After persisting pending check_state rows it re-fetches current page and renders PackageList
// so HTMX swaps in spinner rows without a full page reload.
func checkAllPackages(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	const pageSize = 8
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// parse current page from query so response shows packages from same page the user is on
		page := parsePageQuery(r)

		// enqueue nix eval for every tracked and watched package
		// clears previous check state and persists pending rows before launching goroutines
		err := packages.CheckAll(ctx, db, userID, chk)
		if err != nil {
			writeAppErr(w, "web.checkAllPackages", err)
			return
		}

		// fetch current page of tracked + watched packages
		pkgPage, err := packages.GetPackagesPage(ctx, db, userID, page, pageSize)
		if err != nil {
			writeAppErr(w, "web.checkAllPackages", err)
			return
		}

		// build view models with check state applied so spinner/result rows render correctly on page load
		items, err := buildPackageRowVMs(ctx, db, userID, pkgPage)
		if err != nil {
			writeAppErr(w, "web.indexPage", err)
			return
		}

		// render PackageList
		renderHTML(w, ctx, pages.PackageList(items))
	}
}

// notificationsPage renders the delivery log page with one page of notifications sent to the current user.
func notificationsPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher) http.HandlerFunc {
	const pageSize = 10
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// parse page number from query
		page := parsePageQuery(r)

		// fetch one page of notifications
		logPage, err := notifications.GetDeliveryLogPage(ctx, db, userID, page, pageSize)
		if err != nil {
			writeAppErr(w, "web.notificationsPage", err)
			return
		}

		// get current max retries number from notification dispatcher config
		maxRetries := disp.Config().MaxRetries

		// build pagination URLs
		prevURL, nextURL := buildPaginationURLs(page, logPage.TotalPages, "/")

		// render response
		vms := make([]pages.NotificationLogVM, 0, len(logPage.Notifications))
		for _, n := range logPage.Notifications {
			vms = append(vms, notificationLogVM(n, maxRetries))
		}

		vm := pages.DeliveryLogVM{
			BaseVM:        buildBaseVM(ctx, r, db, sessionManager),
			Notifications: vms,
			Pagination: pages.PaginationVM{
				CurrentPage: logPage.CurrentPage,
				TotalPages:  logPage.TotalPages,
				PrevURL:     prevURL,
				NextURL:     nextURL,
			},
		}

		renderHTML(w, ctx, pages.DeliveryLogPage(vm))
	}
}
