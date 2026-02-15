package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

type VersionVerification struct {
	Name       string `json:"name"`
	StoredVrsn string `json:"storedVersion"`
	CurrVrsn   string `json:"currentVersion"`
	UpToDate   bool   `json:"upToDate"`
}

func RegisterRoutes(mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager) {
	mux.HandleFunc("GET /package", getAllPackages(db))
	mux.HandleFunc("GET /package/check/{id}", checkTrackedPackageVersion(db, sessionManager))
	mux.HandleFunc("POST /package/track/{name}/{branch}", createTracking(db, sessionManager))
	mux.HandleFunc("GET /auth/login", login(provMap, sessionManager))
	mux.HandleFunc("GET /auth/login/specify", specify(sessionManager))
	mux.HandleFunc("GET /auth/logout", logout(sessionManager)) // TODO: make POST
	mux.HandleFunc("GET /auth/callback", callback(db, provMap, sessionManager))
	mux.HandleFunc("GET /", home(sessionManager, db))

	//mux.HandleFunc("GET /package/verify/all", verifyAllUsersPackages(db))
	//mux.HandleFunc("POST /user", createUser(db))
	//mux.HandleFunc("POST /package", createPackage)
	//mux.HandleFunc("DELETE /package", deletePackage)
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

func checkTrackedPackageVersion(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract trackingID from request
		trackingID_string := r.PathValue("id")
		if trackingID_string == "" {
			writeGenericErr(w, "web.checkTrackedPackageVersion", "missing tracking id", errors.New("missing path param id in http request"), http.StatusBadRequest)
			return
		}

		// check tracked package version
		result, err := packages.Check(ctx, db, sessionManager, trackingID_string)
		if err != nil {
			writeAppErr(w, "web.checkTrackedPackageVersion", err)
			return
		}

		// return json reponse with package version compared
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			writeGenericErr(w, "web.checkTrackedPackageVersion", "failed to encode response", err, http.StatusInternalServerError)
			return
		}
	}
}

func createTracking(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract package name and branch from request
		packageName := r.PathValue("name")
		packageBranch := r.PathValue("branch")
		if packageName == "" || packageBranch == "" {
			writeGenericErr(w, "web.createTracking", "missing package name or branch", fmt.Errorf("missing package name or branch - name: '%q' branch: '%q'", packageName, packageBranch), http.StatusBadRequest)
			return
		}

		// create tracking
		err := packages.Track(ctx, db, sessionManager, packageName, packageBranch)
		if err != nil {
			writeAppErr(w, "web.createTracking", err)
			return
		}

		// return reponse
		w.WriteHeader(http.StatusCreated)
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

func logout(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = sessionManager.Destroy(r.Context())
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func home(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := sessionManager.GetUserID(r.Context())
		if uid == 0 {
			fmt.Fprintf(w, "Welcome to the Nixpkgs Notifier!!!\n You are not logged in.\n")
			return
		}
		UserRow, err := db.QueryUserByID(r.Context(), uid)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				writeGenericErr(w, "web.home", "user not found", err, http.StatusNotFound)
				return
			}
			writeGenericErr(w, "web.home", "internal error", err, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "Welcome to the Nixpkgs Notifier!!!\nYour are logged in :)\n id:%d\n username:%s\n role:%s", uid, UserRow.Username, UserRow.Role)
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
