package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/layout"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// loginPage renders the login page.
// Redirects to "/" if user is already logged in.
func loginPage(provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// if user already is logged in just redirect him to home page
		if sessionManager.GetUserID(r.Context()) != 0 {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		// render response
		renderHTML(w, r.Context(), pages.LoginPage(provMap))
	}
}

// login initiates the OIDC authorization code flow for the provider specified by "provider" query parameter.
// It generates an auth URL with a state parameter, stores state in session, and redirects user to the provider.
// Dynamically sets the callback URL based on X-Forwarded-* headers (for reverse proxy support) or falls back to SERVER_URL.
func login(cfg *config.Config, provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 66-79)
		// get provider from query
		providerName := r.URL.Query().Get("provider")
		provider, ok := provMap.Providers[providerName]
		if !ok {
			writeGenericErr(w, "web.login", "unknown provider", fmt.Errorf("unknown provider in http request %q", providerName), http.StatusBadRequest)
			return
		}

		// Dynamically determine server URL and set callback URL on a per-request provider copy
		serverBaseURL := getServerBaseURL(r, cfg)
		providerCopy := *provider
		providerCopy.Config.RedirectURL = serverBaseURL + "/auth/callback"

		// init
		authURL, err := auth.AuthCodeFlowInitLogin(r.Context(), sessionManager, &providerCopy, providerName)
		if err != nil {
			writeAppErr(w, "web.login", err)
			return
		}

		// redirect user to the provider's login page
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// callback handles the OIDC redirect back from provider.
// It validates state and code query parameters, exchanges code for tokens, verifies the ID token,
// and looks up (or creates) the user by issuer+subject.
// On success stores userID in session and redirects to "/".
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

		// pop link data
		// present only when this callback follows /auth/link redirect
		// absent for normal logins (PopLinkData returns false in that case)
		linkData, isLinkFlow := sessionManager.PopLinkData(ctx)

		if isLinkFlow {
			switch linkData.Mode {
			case "new":
				// attach freshly authenticated OIDC identity to the existing logged-in user
				err := users.LinkNewAccount(ctx, db, provider, claims, linkData.UserID)
				if err != nil {
					writeAppErr(w, "web.callback[link-new]", err)
					return
				}
			case "existing":
				// link the authenticated account to the logged-in user (also merges data if the source user becomes orphaned)
				finalRole, err := users.LinkExistingAccount(ctx, db, provider, claims, linkData.UserID)
				if err != nil {
					writeAppErr(w, "web.callback[link-existing]", err)
					return
				}
				// if the merged user was admin, update the role stored in session
				if finalRole == "admin" {
					sessionManager.PutUserRole(ctx, "admin")
				}
			}
			// user is already logged in, session is unchanged - redirect back to accounts
			http.Redirect(w, r, "/accounts", http.StatusFound)
			return
		}

		// get user by issuer, subject (if not found -> new one is created)
		userID, err := users.ResolveOrCreateUser(r.Context(), db, provider, claims)
		if err != nil {
			writeAppErr(w, "web.callback", err)
			return
		}

		// store user id in session
		sessionManager.Put(ctx, "userID", userID)

		// fetch user to get their username and role and store them in session
		user, err := db.QueryUserByID(ctx, userID)
		if err == nil {
			sessionManager.PutUserRole(ctx, user.Role)
			sessionManager.PutUsername(ctx, user.Username)
		}

		// redirect user to the home page
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// logout destroys the current session and redirects user to "/".
func logout(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = sessionManager.Destroy(r.Context())
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// accountsPage renders accounts management page (GET /accounts).
// Shows all accounts linked to the current user and allows linking or unlinking them.
func accountsPage(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// get all accounts linked to the current user
		summary, err := users.GetAccounts(ctx, db, userID)
		if err != nil {
			writeAppErr(w, "web.accountsPage", err)
			return
		}

		// return response
		linkedVMs := make([]pages.LinkedAccountVM, 0, len(summary.Accounts))
		for _, a := range summary.Accounts {
			email := ""
			if a.Email != nil {
				email = *a.Email
			}
			linkedVMs = append(linkedVMs, pages.LinkedAccountVM{
				Provider:      a.Provider,
				Issuer:        a.Issuer,
				Subject:       a.Subject,
				Email:         email,
				EmailVerified: a.EmailVerified,
				LinkedAt:      a.CreatedAt,
				CanUnlink:     summary.CanUnlink,
			})
		}

		vm := pages.AccountsVM{
			BaseVM:   buildBaseVM(ctx, r, db, sessionManager),
			Accounts: linkedVMs,
		}

		renderHTML(w, ctx, pages.AccountsPage(vm))
	}
}

// linkAccount initiates account linking flow.
// Stores link mode and current user ID in the session, then renders the login page
// so user can pick a provider to sign in with. Callback handler completes the flow.
func linkAccount(provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// guard: user must be logged in to link an account
		currentUserID := sessionManager.GetUserID(r.Context())
		if currentUserID == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// mode determines whether to link a new or existing account
		mode := r.URL.Query().Get("mode")
		if mode != "new" && mode != "existing" {
			writeGenericErr(w, "web.linkAccount", "invalid link mode", errors.New("mode must be 'new' or 'existing'"), http.StatusBadRequest)
			return
		}

		// store link context before sending user to pick a provider and logs in
		sessionManager.SaveLinkData(r.Context(), session.LinkData{
			Mode:   mode,
			UserID: currentUserID,
		})

		// render login page directly (user picks provider and logs in as usual, callback function handles the rest)
		renderHTML(w, r.Context(), pages.LoginPage(provMap))
	}
}

// unlinkAccount removes a single OIDC account from the current user.
// Refuses to remove last account (would lock the user out).
func unlinkAccount(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract issuer and subject from submitted form
		issuer := r.FormValue("issuer")
		subject := r.FormValue("subject")
		if issuer == "" || subject == "" {
			writeGenericErr(w, "web.unlinkAccount", "missing issuer or subject", nil, http.StatusBadRequest)
			return
		}

		// unlink the account
		err := users.UnlinkAccount(ctx, db, userID, issuer, subject)
		if err != nil {
			writeAppErr(w, "web.unlinkAccount", err)
			return
		}

		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
	}
}

// updateProfileUsername handles profile username change form (POST /profile/username).
func updateProfileUsername(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get user ID
		userID := sessionManager.GetUserID(r.Context())

		// extract username from submitted form
		username := r.FormValue("username")

		// update username
		err := users.UpdateUsername(ctx, db, userID, username)
		if err != nil {
			writeAppErr(w, "web.updateProfileUsername", err)
			return
		}

		// save new username in session
		sessionManager.PutUsername(ctx, strings.TrimSpace(username))

		// return response
		renderHTML(w, ctx, layout.ProfileNameDisplay(strings.TrimSpace(username)))
	}
}
