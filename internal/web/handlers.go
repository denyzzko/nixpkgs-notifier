package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

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

		vm := pages.IndexVM{Packages: pkgVMs}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.IndexPage(vm).Render(ctx, w)
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

func logout(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = sessionManager.Destroy(r.Context())
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func verifyTrackedPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
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
		result, err := packages.Check(ctx, db, sessionManager, chk, packageID_string)
		if err != nil {
			writeAppErr(w, "web.checkTrackedPackageVersion", err)
			return
		}

		// render reponse
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.TrackedPackageItem(trackedPackageVM(result)).Render(ctx, w)
	}
}

func verifyAllTrackedPackages(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()

		// check all tracked package versions
		results, err := packages.CheckAll(ctx, db, sessionManager, chk)
		if err != nil {
			writeAppErr(w, "web.verifyAllPackages", err)
			return
		}

		// render response
		pkgVMs := make([]pages.TrackedPackageVM, 0, len(results))
		for _, result := range results {
			pkgVMs = append(pkgVMs, trackedPackageVM(result))
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

func trackPackage(db *database.Store, sessionManager *session.SessionManager, chk *checker.Checker) http.HandlerFunc {
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
		if err := packages.Track(ctx, db, sessionManager, chk, packageName, packageBranch); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, appError.PublicMessage(err)).Render(ctx, w)
			return
		}

		// reload all tracked packages
		tracked, err := packages.GetTrackedPackages(ctx, db, sessionManager)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewPackageError(packageName, packageBranch, "Package tracked but failed to reload. Please refresh.").Render(ctx, w)
			return
		}

		// find the one that was just added and render it
		for _, t := range tracked {
			if t.Name == packageName && t.Branch == packageBranch {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_ = pages.TrackedPackageItem(trackedPackageVMFromTracked(t)).Render(ctx, w)
				return
			}
		}

		// fallback
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.NewPackageError(packageName, packageBranch, "Package tracked but could not be found. Please try to refresh the page.").Render(ctx, w)
	}
}

func channelsPage(sessionManager *session.SessionManager, db *database.Store) http.HandlerFunc {
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

		// render response
		chVMs := make([]pages.ChannelVM, 0, len(chnls))
		for _, ch := range chnls {
			chVMs = append(chVMs, channelVM(ch))
		}
		vm := pages.ChannelsVM{Channels: chVMs}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ChannelsPage(vm).Render(ctx, w)
	}
}

func addChannelForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// render response
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.NewChannelForm().Render(r.Context(), w)
	}
}

func addChannelFormCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// empty response body — HTMX clears input item slot
		w.WriteHeader(http.StatusOK)
	}
}

func addChannel(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// extract channel type, adress and flag value from submitted form
		chType := r.FormValue("type")
		address := r.FormValue("address")
		notifyOnManualVerify := r.FormValue("notify_on_manual_verify") == "on"
		if chType != "email" && chType != "webhook" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(chType, address, "Invalid channel type.").Render(ctx, w)
			return
		}
		if address == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(chType, address, "Address is required.").Render(ctx, w)
			return
		}

		// add channel
		ch, err := channels.AddChannel(ctx, db, sessionManager, chType, address, notifyOnManualVerify)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.NewChannelError(chType, address, appError.PublicMessage(err)).Render(ctx, w)
			return
		}

		// render response
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ChannelItem(channelVM(ch)).Render(ctx, w)
	}
}

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

func toggleChannelEnabled(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID and toggle value from request
		channelID_string := r.PathValue("id")
		if channelID_string == "" {
			writeGenericErr(w, "web.toggleChannelEnabled", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelID_string, 10, 64)
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ChannelItem(channelVM(ch)).Render(ctx, w)
	}
}

func toggleNotifyOnManualVerify(db *database.Store, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// extract channel ID and toggle value from request
		channelID_string := r.PathValue("id")
		if channelID_string == "" {
			writeGenericErr(w, "web.toggleNotifyOnManualVerify", "missing channel id", errors.New("missing path param id"), http.StatusBadRequest)
			return
		}
		value := r.FormValue("value") == "true"

		// convert channel ID string to int64
		channelID, err := strconv.ParseInt(channelID_string, 10, 64)
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ChannelItem(channelVM(ch)).Render(ctx, w)
	}
}

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
		var emailAddress *string
		var webhookURL *string
		if ch.Type == "Email" {
			emailAddress = &ch.Address
		} else {
			webhookURL = &ch.Address
		}

		// send test message (sync, no notifications table entry)
		testErr := disp.Test(ctx, channelID, emailAddress, webhookURL)

		// render channel back with the result message
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if testErr != nil {
			_ = pages.ChannelItemWithMessage(channelVM(ch), "text-danger small", "Test failed: "+notify.PublicMessage(testErr)).Render(ctx, w)
		} else {
			_ = pages.ChannelItemWithMessage(channelVM(ch), "text-success small", "Test message sent successfully.").Render(ctx, w)
		}
	}
}

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

		vm := pages.DeliveryLogVM{Notifications: vms}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.DeliveryLogPage(vm).Render(ctx, w)
	}
}
