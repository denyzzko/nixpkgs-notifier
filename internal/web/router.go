package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

func RegisterRoutes(mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager) {
	mux.HandleFunc("GET /", homePage(sessionManager, db))

	mux.HandleFunc("GET /login", loginPage())
	mux.HandleFunc("GET /auth/login", login(provMap, sessionManager))
	mux.HandleFunc("GET /auth/callback", callback(db, provMap, sessionManager))
	mux.HandleFunc("GET /auth/logout", logout(sessionManager)) // TODO: make POST

	mux.HandleFunc("POST /package/verify/{id}", verifyTrackedPackage(db, sessionManager))
	mux.HandleFunc("POST /package/verify/all", verifyAllTrackedPackages(db, sessionManager))

	mux.HandleFunc("POST /package/untrack/{id}", untrackPackage(db, sessionManager))
	mux.HandleFunc("GET /package/track/form", trackPackageForm())
	mux.HandleFunc("GET /package/track/cancel", trackPackageFormCancel())
	mux.HandleFunc("POST /package/track", trackPackage(db, sessionManager))

	//mux.HandleFunc("POST /package/track/{name}/{branch}", trackPackage(db, sessionManager))
	//mux.HandleFunc("GET /package/check/{id}", checkTrackedPackageVersion(db, sessionManager))

	//mux.HandleFunc("GET /auth/login/specify", specify(sessionManager))
	//mux.HandleFunc("GET /package", getAllPackages(db))
	//mux.HandleFunc("GET /package/verify/all", verifyAllUsersPackages(db))
	//mux.HandleFunc("POST /user", createUser(db))
	//mux.HandleFunc("POST /package", createPackage)
	//mux.HandleFunc("DELETE /package", deletePackage)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func renderPage(w http.ResponseWriter, r *http.Request, ctx context.Context, partial templ.Component, full templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var component templ.Component
	if isHTMX(r) {
		component = partial
	} else {
		component = full
	}
	if err := component.Render(ctx, w); err != nil {
		log.Printf("[ERROR] render failed: %v", err)
	}
}

func homePage(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user from session
		uid := sessionManager.GetUserID(ctx)
		if uid == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// get all packages this user tracks
		tracked, err := packages.GetTrackedPackages(ctx, db, uid)
		if err != nil {
			writeAppErr(w, "web.homePage", err)
			return
		}

		// render response
		pkgVMs := make([]pages.TrackedPackageVM, 0, len(tracked))
		for _, t := range tracked {
			pkgVMs = append(pkgVMs, pages.TrackedPackageVM{
				PackageID:           t.PackageID,
				Name:                t.Name,
				Branch:              t.Branch,
				LastNotifiedVersion: t.LastNotifiedVersion,
			})
		}

		vm := pages.HomeVM{Packages: pkgVMs}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.HomePage(vm).Render(ctx, w)
	}
}

func loginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.LoginPage().Render(r.Context(), w)
	}
}

func login(provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 66-79)
		// get provider from query
		providerName := r.URL.Query().Get("provider")
		provider, ok := provMap.Providers[providerName]
		if !ok {
			writeGenericErr(w, "web.login", "unknown provider", fmt.Errorf("unknown provider in http request %q", providerName), http.StatusBadRequest)
			return
		}

		// init
		authURL, err := auth.AuthCodeFlowInitLogin(r.Context(), sessionManager, provider, providerName)
		if err != nil {
			writeAppErr(w, "web.login", err)
			return
		}

		// redirect user to the provider's login page
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func callback(db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 82-136)
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// get state from query parameter
		state := r.URL.Query().Get("state")
		if state == "" {
			writeGenericErr(w, "web.callback", "missing authorization state", errors.New("missing query param state in OIDC flow (on callback)"), http.StatusBadRequest)
			return
		}

		// get authorization code from query parameter
		code := r.URL.Query().Get("code")
		if code == "" {
			writeGenericErr(w, "web.callback", "missing authorization code", errors.New("missing query param code in OIDC flow (on callback)"), http.StatusBadRequest)
			return
		}

		// exchange authorization code for tokens, extract ID token from response, verify ID token and extract user claims
		claims, provider, err := auth.AuthCodeFlowCallback(ctx, sessionManager, provMap, state, code)
		if err != nil {
			writeAppErr(w, "web.callback", err)
			return
		}

		// get user by issuer, subject (if not found -> new one is created)
		userID, err := users.GetUser(r.Context(), db, provider, claims)
		if err != nil {
			writeAppErr(w, "web.callback", err)
			return
		}

		// store user id in session
		sessionManager.Put(ctx, "userID", userID)

		// redirect user to the home page
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func verifyTrackedPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()

		// extract package ID from request
		packageID_string := r.PathValue("id")
		if packageID_string == "" {
			writeGenericErr(w, "web.verifyPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// check tracked package version
		result, err := packages.Check(ctx, db, sessionManager, packageID_string)
		if err != nil {
			writeAppErr(w, "web.checkTrackedPackageVersion", err)
			return
		}

		// render reponse
		vm := pages.TrackedPackageVM{
			PackageID:           result.PackageID,
			Name:                result.Name,
			Branch:              result.Branch,
			LastNotifiedVersion: result.LastNotifiedVersion,
			CurrentVersion:      result.CurrentVersion,
			VersionChanged:      result.VersionChanged,
			Verified:            true,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.TrackedPackageItem(vm).Render(ctx, w)
	}
}

func verifyAllTrackedPackages(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()

		// check all tracked package versions
		results, err := packages.CheckAll(ctx, db, sessionManager)
		if err != nil {
			writeAppErr(w, "web.verifyAllPackages", err)
			return
		}

		// render response
		pkgVMs := make([]pages.TrackedPackageVM, 0, len(results))
		for _, result := range results {
			pkgVMs = append(pkgVMs, pages.TrackedPackageVM{
				PackageID:           result.PackageID,
				Name:                result.Name,
				Branch:              result.Branch,
				LastNotifiedVersion: result.LastNotifiedVersion,
				CurrentVersion:      result.CurrentVersion,
				VersionChanged:      result.VersionChanged,
				Verified:            true,
			})
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.TrackedPackageList(pkgVMs).Render(ctx, w)
	}
}

func untrackPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract package ID from request
		packageID_string := r.PathValue("id")
		if packageID_string == "" {
			writeGenericErr(w, "web.untrackPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// delete tracking
		if err := packages.Untrack(ctx, db, sessionManager, packageID_string); err != nil {
			writeAppErr(w, "web.untrackPackage", err)
			return
		}

		// empty response body - HTMX clears the item
		w.WriteHeader(http.StatusOK)
	}
}

func trackPackageForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.NewPackageForm().Render(r.Context(), w)
	}
}

func trackPackageFormCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// empty response body — HTMX clears input item slot
		w.WriteHeader(http.StatusOK)
	}
}

func trackPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()

		// extract package name and branch from submitted form
		packageName := r.FormValue("name")
		packageBranch := r.FormValue("branch")

		if packageName == "" || packageBranch == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, "Package name and branch are required.").Render(ctx, w)
			return
		}

		// track package
		if err := packages.Track(ctx, db, sessionManager, packageName, packageBranch); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, appError.PublicMessage(err)).Render(ctx, w)
			return
		}

		// reload all tracked packages
		uid := sessionManager.GetUserID(ctx)
		tracked, err := packages.GetTrackedPackages(ctx, db, uid)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, "Package tracked but failed to reload. Please refresh.").Render(ctx, w)
			return
		}

		// find the one that was just added and render it
		for _, t := range tracked {
			if t.Name == packageName && t.Branch == packageBranch {
				vm := pages.TrackedPackageVM{
					PackageID:           t.PackageID,
					Name:                t.Name,
					Branch:              t.Branch,
					LastNotifiedVersion: t.LastNotifiedVersion,
					CurrentVersion:      t.LastNotifiedVersion,
					Verified:            true,
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_ = pages.TrackedPackageItem(vm).Render(ctx, w)
				return
			}
		}

		// fallback
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.NewPackageError(packageName, packageBranch, "Package tracked but could not be found. Please try to refresh the page.").Render(ctx, w)
	}
}

func logout(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = sessionManager.Destroy(r.Context())
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

/*
func trackPackage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract package name and branch from request
		packageName := r.PathValue("name")
		packageBranch := r.PathValue("branch")
		if packageName == "" || packageBranch == "" {
			writeGenericErr(w, "web.trackPackage", "missing package name or branch", fmt.Errorf("missing package name or branch - name: '%q' branch: '%q'", packageName, packageBranch), http.StatusBadRequest)
			return
		}

		// create tracking
		err := packages.Track(ctx, db, sessionManager, packageName, packageBranch)
		if err != nil {
			log.Printf("[ERROR] web.trackPackage: %v", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.PackageItemError(packageName, packageBranch, appError.PublicMessage(err)).Render(ctx, w)
			return
		}

		// return reponse (just re-renders row as "tracked")
		vm := pages.PackageResultVM{Name: packageName, Branch: packageBranch, Version: "…", Tracked: true} // TODO: get package version to be put here

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.PackageItem(vm).Render(ctx, w)
	}
}


func getAllPackages(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		// get all packages
		packages, err := packages.GetAll(ctx, db)
		if err != nil {
			writeAppErr(w, "web.getAllPackages", err)
			return
		}

		// return json response with packages
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(packages); err != nil {
			// IDK about this maybe it wont be used anyway since SSR will be used instead of json probably
			// currently needed because writeErr cant be used here since its not app error (maybe it could be wrapped to appError here though :)...)
			// either way after this error system wont probably be able to send error message to user reliably so idkk maybe just log here
			// ... i just didnt want to have those two lines here (log & error)
			writeGenericErr(w, "web.getAllPackages", "failed to encode response", err, http.StatusInternalServerError)
			return
		}
	}
}

func specify(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := sessionManager.GetUserID(r.Context())
		if uid == 0 {
			fmt.Fprintf(w, "You are not logged in.\n")
			return
		}
		fmt.Fprintf(w, "Welcome to the Nixpkgs Notifier!!!\n This is your first login. Please specify your account details:\n username: XXX\n email:XXX")
	}
}
*/
