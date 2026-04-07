package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/layout"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// getServerBaseURL reconstructs the server's base URL from the request.
// If X-Forwarded-Proto and X-Forwarded-Host headers are present (from reverse proxy),
// uses them. Otherwise it uses the incoming request host, and only then
// falls back to cfg.ServerURL.
// Returns base URL without trailing slash (e.g., "https://example.com", "http://localhost:8080").
func getServerBaseURL(r *http.Request, cfg *config.Config) string {
	// Check for reverse proxy headers
	proto := r.Header.Get("X-Forwarded-Proto")
	host := r.Header.Get("X-Forwarded-Host")

	if proto != "" && host != "" {
		// Proxies may send multiple values; use the first hop.
		proto = strings.TrimSpace(strings.Split(proto, ",")[0])
		host = strings.TrimSpace(strings.Split(host, ",")[0])

		// Ensure host doesn't already have protocol
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimPrefix(host, "https://")
		return proto + "://" + host
	}

	// Use direct request host when app is accessed without reverse proxy.
	if r.Host != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host = strings.TrimPrefix(r.Host, "http://")
		host = strings.TrimPrefix(host, "https://")
		return scheme + "://" + host
	}

	// Fallback to configured SERVER_URL
	return strings.TrimSuffix(cfg.ServerURL, "/")
}

// renderHTML sets the Content-Type header to text/html and renders the given templ component.
func renderHTML(w http.ResponseWriter, ctx context.Context, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(ctx, w)
}

// indexPage renders the home page with all packages current user is tracking.
func indexPage(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get all packages this user tracks
		tracked, err := packages.GetTrackedPackages(ctx, db, sessionManager)
		if err != nil {
			writeAppErr(w, "web.indexPage", err)
			return
		}

		// render response
		pkgVMs := make([]pages.TrackedPackageVM, 0, len(tracked))
		for _, t := range tracked {
			pkgVMs = append(pkgVMs, trackedPackageVMFromTracked(t))
		}

		vm := pages.IndexVM{
			Packages: pkgVMs,
			Username: sessionManager.GetUsername(r.Context()),
			Role:     sessionManager.GetUserRole(r.Context()),
			IsAdmin:  sessionManager.GetUserRole(r.Context()) == "admin",
		}

		renderHTML(w, ctx, pages.IndexPage(vm))
	}
}

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

		// get user by issuer, subject (if not found -> new one is created)
		userID, err := users.GetUser(r.Context(), db, provider, claims)
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

// checkTrackedPackage handles a manual version check for a tracked package (POST /package/check/{id}).
// If the package was checked recently the actual check is skipped and current row is rendered immediately with the latest stored version.
// Otherwise a background nix eval is enqueued and a loading row with a polling URL is returned so HTMX
// can poll status endpoint until eval completes.
func checkTrackedPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract package ID from request
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.checkTrackedPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// enqueue async check
		outcome, err := packages.Check(ctx, db, sessionManager, chk, packageIDStr)
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

		// build polling URL - embeds prev so status endpoint can show version transition
		pollingURL := fmt.Sprintf("/package/status/check/%s?prev=%s", packageIDStr, url.QueryEscape(outcome.Package.LastNotifiedVersion))

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

		// extract package ID from request
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.untrackPackage", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// delete tracking
		if err := packages.Untrack(ctx, db, sessionManager, packageIDStr); err != nil {
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

		// extract package name and branch from submitted form
		packageName := r.FormValue("name")
		packageBranch := r.FormValue("branch")

		if packageName == "" || packageBranch == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, "Package name and branch are required.").Render(ctx, w)
			return
		}

		// store tracking immediately - nix eval runs in background goroutine
		trackedPackage, err := packages.Track(ctx, db, sessionManager, chk, packageName, packageBranch)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, appError.PublicMessage(err)).Render(ctx, w)
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

		// extract package id
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.packageTrackStatus", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// get status of package tracking initialization
		status, err := packages.GetTrackStatus(ctx, db, sessionManager, packageIDStr)
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
		// nix eval failed - show error row with untrack button
		if status.Failed {
			vm := trackedPackageVMFromTracked(status.Package)
			vm.ErrMsg = status.ErrMsg
			renderHTML(w, ctx, pages.TrackedPackageItemInitError(vm))
			return
		}
		// completed with success
		renderHTML(w, ctx, pages.TrackedPackageItem(trackedPackageVMFromTracked(status.Package)))
	}
}

// packageCheckStatus is the polling endpoint for manual package checks.
// It is called every 3s by the checking row rendered after POST /package/check/{id}.
// Query params: prev (last_notified_version before check).
// Returns a spinner row (with polling) while nix eval is in progress.
// Returns final row (without polling) once operationResults map signals completion.
func packageCheckStatus(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract package id
		packageIDStr := r.PathValue("id")
		if packageIDStr == "" {
			writeGenericErr(w, "web.packageCheckStatus", "missing package id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// extract prev version from query params
		prev := r.URL.Query().Get("prev")

		// get status of check operation
		status, err := packages.GetCheckStatus(ctx, db, sessionManager, packageIDStr, prev)
		if err != nil {
			writeAppErr(w, "web.packageCheckStatus", err)
			return
		}

		// build polling URL, preserving prev so the status endpoint can compute version transitions
		pollingURL := fmt.Sprintf("/package/status/check/%s?prev=%s", packageIDStr, url.QueryEscape(prev))

		// still running - render loading row (HTMX will poll again in 3s)
		if !status.Done {
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
			PackageID:           status.Package.PackageID,
			Name:                status.Package.Name,
			Branch:              status.Package.Branch,
			LastNotifiedVersion: status.Prev,
			CurrentVersion:      status.Package.LastNotifiedVersion,
			VersionChanged:      status.VersionChanged,
			Verified:            true,
		}
		renderHTML(w, ctx, pages.TrackedPackageItem(vm))
	}
}

// channelsPage renders the notification channels page with all channels current user has configured.
func channelsPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get all channels this user has
		chnls, err := channels.GetChannels(ctx, db, sessionManager)
		if err != nil {
			writeAppErr(w, "web.channelsPage", err)
			return
		}

		// get value of MaxRetries
		maxRetries := disp.MaxRetries()

		// render response
		chVMs := make([]pages.ChannelVM, 0, len(chnls))
		for _, ch := range chnls {
			chVMs = append(chVMs, channelVM(ch, maxRetries))
		}

		vm := pages.ChannelsVM{
			Channels: chVMs,
			Username: sessionManager.GetUsername(r.Context()),
			Role:     sessionManager.GetUserRole(r.Context()),
			IsAdmin:  sessionManager.GetUserRole(r.Context()) == "admin",
		}

		renderHTML(w, ctx, pages.ChannelsPage(vm))
	}
}

// addChannelForm renders the inline form for adding a new notification channel (GET /channel/add/form).
func addChannelForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		renderHTML(w, r.Context(), pages.NewChannelForm())
	}
}

// addChannelFormCancel clears inline new-channel form slot (GET /channel/add/cancel).
// Responds with empty 200 so HTMX removes the form.
func addChannelFormCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// empty response body — HTMX clears input item slot
		w.WriteHeader(http.StatusOK)
	}
}

// addChannel creates a new notification channel (email or webhook) for the current user (POST /channel/add).
// Reads type, address, notify_on_manual_verify and optional matermost webhook info from the submitted form.
// On success renders new channel row.
// On validation or application error re-renders form with an error message.
func addChannel(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract channel type, adress, notify_on_manual_verify flag and mattermost webhook info value from submitted form
		rawType := r.FormValue("type")
		address := r.FormValue("address")
		notifyOnManualVerify := r.FormValue("notify_on_manual_verify") == "on"
		username := r.FormValue("username")
		channel := r.FormValue("channel")
		priority := r.FormValue("priority")
		requestAck := r.FormValue("request_ack") == "true"

		// decode type into chType + webhookType
		var chType, webhookType string
		switch rawType {
		case "email":
			chType, webhookType = "email", ""
		case "webhook_generic":
			chType, webhookType = "webhook", "generic"
		case "webhook_mattermost":
			chType, webhookType = "webhook", "mattermost"
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(rawType, address, "Invalid channel type.").Render(ctx, w)
			return
		}
		if address == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(rawType, address, "Address is required.").Render(ctx, w)
			return
		}

		// add channel
		ch, err := channels.AddChannel(ctx, db, sessionManager, chType, address, webhookType, notifyOnManualVerify, username, channel, priority, requestAck)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(rawType, address, appError.PublicMessage(err)).Render(ctx, w)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// deleteChannel removes a notification channel by ID (POST /channel/delete/{id}).
// Responds with empty 200 so HTMX swaps the row out of the list.
func deleteChannel(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID from request
		channelID := r.PathValue("id")
		if channelID == "" {
			writeGenericErr(w, "web.deleteChannel", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// delete channel
		if err := channels.DeleteChannel(ctx, db, sessionManager, channelID); err != nil {
			writeAppErr(w, "web.deleteChannel", err)
			return
		}

		// empty response body - HTMX clears the item
		w.WriteHeader(http.StatusOK)
	}
}

// toggleChannelEnabled sets the enabled flag on a channel (POST /channel/toggle/{id}).
// Reads the desired boolean state from "value" form field and re-renders the channel row.
func toggleChannelEnabled(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID and toggle value from request
		channelIDStr := r.PathValue("id")
		if channelIDStr == "" {
			writeGenericErr(w, "web.toggleChannelEnabled", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.toggleChannelEnabled", "invalid channel id", err, http.StatusBadRequest)
			return
		}

		// toggle enabled flag
		ch, err := channels.ToggleEnabled(ctx, db, sessionManager, channelID, value)
		if err != nil {
			writeAppErr(w, "web.toggleChannelEnabled", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// toggleNotifyOnManualVerify sets the notify_on_manual_verify flag on a channel (POST /channel/toggle-notify/{id}).
// When enabled, a notification is sent for manual checks if version has changed.
// Reads the desired boolean state from "value" form field and re-renders the channel row.
func toggleNotifyOnManualVerify(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID and toggle value from request
		channelIDStr := r.PathValue("id")
		if channelIDStr == "" {
			writeGenericErr(w, "web.toggleNotifyOnManualVerify", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.toggleNotifyOnManualVerify", "invalid channel id", err, http.StatusBadRequest)
			return
		}

		// toggle notify_on_manual_verify flag
		ch, err := channels.ToggleNotifyOnManualVerify(ctx, db, sessionManager, channelID, value)
		if err != nil {
			writeAppErr(w, "web.toggleNotifyOnManualVerify", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, 0)))
	}
}

// acknowledgeChannelDisabled clears "disabled by server" warning for channel (POST /channel/ack-disabled/{id}).
// Channel remains disabled, warning banner is removed and row renders normally.
func acknowledgeChannelDisabled(db *database.Store, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID from request
		channelIDStr := r.PathValue("id")
		if channelIDStr == "" {
			writeGenericErr(w, "web.acknowledgeChannelDisabled", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.acknowledgeChannelDisabled", "invalid channel id", err, http.StatusBadRequest)
			return
		}

		// clear "disabled by server" flag
		ch, err := channels.AcknowledgeDisabled(ctx, db, sessionManager, channelID)
		if err != nil {
			writeAppErr(w, "web.acknowledgeChannelDisabled", err)
			return
		}

		// render response
		renderHTML(w, ctx, pages.ChannelItem(channelVM(ch, disp.MaxRetries())))
	}
}

// testChannel sends a test message through the specified channel (POST /channel/test/{id}).
// The test is synchronous and does not create a notifications table entry.
// Re-renders the channel row with a success or failure message inline.
func testChannel(db *database.Store, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// extract channel ID from request
		channelIDStr := r.PathValue("id")
		if channelIDStr == "" {
			writeGenericErr(w, "web.testChannel", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.testChannel", "invalid channel id", err, http.StatusBadRequest)
			return
		}

		// fetch channel info
		ch, err := channels.GetChannelByID(ctx, db, sessionManager, channelID)
		if err != nil {
			writeAppErr(w, "web.testChannel", err)
			return
		}

		// resolve address
		var email *database.Email
		var webhook *database.Webhook

		if ch.Type == "Email" {
			email = &database.Email{Address: ch.Address}
		} else {
			webhook = &database.Webhook{
				URL:        ch.Address,
				Type:       ch.WebhookType,
				Username:   ch.WebhookUsername,
				Channel:    ch.WebhookChannel,
				Priority:   ch.WebhookPriority,
				RequestAck: ch.WebhookRequestAck,
			}
		}
		// send test message (sync, no notifications table entry)
		testErr := disp.Test(ctx, channelID, email, webhook)

		// render channel back with the result message
		if testErr != nil {
			renderHTML(w, ctx, pages.ChannelItemWithMessage(channelVM(ch, 0), "text-danger small", "Test failed: "+notify.PublicMessage(testErr)))
		} else {
			renderHTML(w, ctx, pages.ChannelItemWithMessage(channelVM(ch, 0), "text-success small", "Test message sent successfully."))
		}
	}
}

// notificationsPage renders the delivery log page with all notifications sent to the current user.
func notificationsPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get all notifications this user has
		logs, err := notifications.GetDeliveryLog(ctx, db, sessionManager)
		if err != nil {
			writeAppErr(w, "web.notificationsPage", err)
			return
		}

		// get current max retries number from notification dispatcher config
		maxRetries := disp.MaxRetries()

		// render response
		vms := make([]pages.NotificationLogVM, 0, len(logs))
		for _, n := range logs {
			vms = append(vms, notificationLogVM(n, maxRetries))
		}

		vm := pages.DeliveryLogVM{
			Notifications: vms,
			Username:      sessionManager.GetUsername(r.Context()),
			Role:          sessionManager.GetUserRole(r.Context()),
			IsAdmin:       sessionManager.GetUserRole(r.Context()) == "admin",
		}

		renderHTML(w, ctx, pages.DeliveryLogPage(vm))
	}
}

// systemConfigPage renders the admin system configuration page with current runtime values.
func systemConfigPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get current runtime config
		rc := config.GetRuntimeConfig(ctx, db, disp, chk)

		// render response
		vm := systemConfigVM(rc.Dispatcher, rc.Checker)
		vm.Saved = r.URL.Query().Get("saved") == "1"
		vm.Username = sessionManager.GetUsername(r.Context())
		vm.Role = sessionManager.GetUserRole(r.Context())
		renderHTML(w, ctx, pages.SystemConfigPage(vm))
	}
}

// updateSystemConfig handles POST from the admin system config form.
// Parses, persists, and applies the new runtime configuration.
func updateSystemConfig(db *database.Store, disp *dispatcher.Dispatcher, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// parse and validate runtime config from the submitted form
		rcfg, err := config.RuntimeConfigFromForm(r)
		if err != nil {
			writeGenericErr(w, "web.updateSystemConfig", err.Error(), err, http.StatusBadRequest)
			return
		}

		// store config to database and apply dispatcher and checker
		if err := config.SaveRuntimeConfig(ctx, db, rcfg, disp, chk); err != nil {
			writeAppErr(w, "web.updateSystemConfig", err)
			return
		}

		http.Redirect(w, r, "/admin/config?saved=1", http.StatusSeeOther)
	}
}

// updateProfileUsername handles profile username change form (POST /profile/username).
func updateProfileUsername(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract username from submitted form
		username := r.FormValue("username")

		// update username
		if err := users.UpdateUsername(ctx, db, sessionManager, username); err != nil {
			writeAppErr(w, "web.updateProfileUsername", err)
			return
		}

		// save new username in session
		sessionManager.PutUsername(ctx, strings.TrimSpace(username))

		// return response
		renderHTML(w, ctx, layout.ProfileNameDisplay(strings.TrimSpace(username)))
	}
}

// profilesPage renders the admin profile management page with all users in the system.
func profilesPage(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get all users
		usrs, err := db.QueryAllUsers(ctx)
		if err != nil {
			writeAppErr(w, "web.profilesPage", err)
			return
		}

		// return response
		vm := pages.ProfilesVM{
			Profiles: make([]pages.ProfileVM, 0, len(usrs)),
			Username: sessionManager.GetUsername(r.Context()),
			Role:     sessionManager.GetUserRole(r.Context()),
			IsAdmin:  true,
		}
		for _, u := range usrs {
			vm.Profiles = append(vm.Profiles, pages.ProfileVM{
				ID:        u.ID,
				Username:  u.Username,
				Role:      u.Role,
				CreatedAt: u.CreatedAt,
			})
		}
		renderHTML(w, ctx, pages.ProfilesPage(vm))
	}
}

// profileEditForm returns inline edit form for profile row.
func profileEditForm(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract user ID from request and convert string to int64
		idStr := r.PathValue("id")
		if idStr == "" {
			writeGenericErr(w, "web.profileEditForm", "missing user id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.profileEditForm", "invalid user id", err, http.StatusBadRequest)
			return
		}

		// query user by id
		usr, err := db.QueryUserByID(ctx, id)
		if err != nil {
			writeAppErr(w, "web.profileEditForm", err)
			return
		}

		// return response
		renderHTML(w, ctx, pages.ProfileEditForm(pages.ProfileVM{
			ID:        usr.ID,
			Username:  usr.Username,
			Role:      usr.Role,
			CreatedAt: usr.CreatedAt,
		}))
	}
}

// profileEditCancel returns normal profile row, cancelling in-progress edit.
func profileEditCancel(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract user ID from request and convert string to int64
		idStr := r.PathValue("id")
		if idStr == "" {
			writeGenericErr(w, "web.profileEditCancel", "missing user id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.profileEditCancel", "invalid user id", err, http.StatusBadRequest)
			return
		}

		// query user by id
		usr, err := db.QueryUserByID(ctx, id)
		if err != nil {
			writeAppErr(w, "web.profileEditCancel", err)
			return
		}

		// return response
		renderHTML(w, ctx, pages.ProfileItem(pages.ProfileVM{
			ID:        usr.ID,
			Username:  usr.Username,
			Role:      usr.Role,
			CreatedAt: usr.CreatedAt,
		}))
	}
}

// updateProfile applies username and role changes to a user (POST /admin/profiles/{id}).
func updateProfile(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract and parse user ID from request
		idStr := r.PathValue("id")
		if idStr == "" {
			writeGenericErr(w, "web.updateProfile", "missing user id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeGenericErr(w, "web.updateProfile", "invalid user id", err, http.StatusBadRequest)
			return
		}

		// extract username and role from submitted form
		newUsername := r.FormValue("username")
		newRole := r.FormValue("role")

		// validate and persist changes
		// u holds pre-update state for error re-rendering
		u, err := users.UpdateUsernameAndRole(ctx, db, id, newUsername, newRole)
		if err != nil {
			renderHTML(w, ctx, pages.ProfileUpdateError(pages.ProfileVM{
				ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt,
			}, appError.PublicMessage(err)))
			return
		}

		// return response
		renderHTML(w, ctx, pages.ProfileItem(pages.ProfileVM{
			ID:        u.ID,
			Username:  strings.TrimSpace(newUsername),
			Role:      newRole,
			CreatedAt: u.CreatedAt,
		}))
	}
}
