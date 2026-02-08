package web

import (
	"context"
	"encoding/json"
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
	mux.HandleFunc("GET /package/check/{trackingID}", checkTrackedPackageVersion(db, sessionManager))
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
		pkgs, err := packages.GetAll(ctx, db)
		if err != nil {
			http.Error(w, "failed to get all packages: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// return json response with packages
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pkgs); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
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
		trackingID_string := r.PathValue("trackingID")
		if trackingID_string == "" {
			http.Error(w, "missing package tracking identifier in http query", http.StatusBadRequest)
			return
		}

		// check tracked package version
		result, err := packages.Check(ctx, db, sessionManager, trackingID_string)
		if err != nil {
			// return specific error
			if err.Error() == "you are not tracking this package" || err.Error() == "package not found" {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			} else if err.Error() == "not authenticated" {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			} else if err.Error() == "failed to get package version from Nix" {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// return json reponse with package version compared
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
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
			http.Error(w, "missing package name or branch in http query", http.StatusBadRequest)
			return
		}

		// create tracking
		err := packages.Track(ctx, db, sessionManager, packageName, packageBranch)
		if err != nil {
			// return specific error
			if err.Error() == "failed to get package version from Nix" {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			} else if err.Error() == "not authenticated" {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}

		authURL, err := auth.AuthCodeFlowInitLogin(r.Context(), sessionManager, provider, providerName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
			http.Error(w, "missing authorization state", http.StatusBadRequest)
			return
		}

		// get authorization code from query parameter
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}

		// exchange authorization code for tokens, extract ID token from response, verify ID token and extract user claims
		claims, provider, err := auth.AuthCodeFlowCallback(ctx, sessionManager, provMap, state, code)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError) //TODO: currently just one type of error but AuthCodeFlowCallback should be able to return different types (internal, bad request, ...)
			return
		}

		// get user by issuer, subject (if not found -> new one is created)
		userID, err := users.GetUser(r.Context(), db, provider, claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
			if err == database.ErrNotFound {
				http.Error(w, "failed to find you, you do not exist :)", http.StatusNotFound)
			} else {
				http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
			}
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
