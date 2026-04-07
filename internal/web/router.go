package web

import (
	"net/http"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

// RegisterRoutes registers all HTTP routes on mux.
// Each handler receives only the dependencies it needs.
func RegisterRoutes(mux *http.ServeMux, cfg *config.Config, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher, chk *checker.Checker) {
	// home page (displays all tracked packages)
	mux.HandleFunc("GET /", requireAuth(sessionManager, indexPage(sessionManager, db)))

	// login page and corresponding routes
	mux.HandleFunc("GET /login", loginPage(provMap, sessionManager))
	mux.HandleFunc("GET /auth/login", login(cfg, provMap, sessionManager))
	mux.HandleFunc("GET /auth/callback", callback(db, provMap, sessionManager))
	mux.HandleFunc("POST /auth/logout", logout(sessionManager))

	// routes for package operations (package verifications, track/untrack)
	mux.HandleFunc("POST /package/check/{id}", checkTrackedPackage(db, sessionManager, chk))
	mux.HandleFunc("POST /package/untrack/{id}", untrackPackage(db, sessionManager))
	mux.HandleFunc("GET /package/track/form", trackPackageForm())
	mux.HandleFunc("GET /package/track/cancel", trackPackageFormCancel())
	mux.HandleFunc("POST /package/track", trackPackage(db, sessionManager, chk))
	mux.HandleFunc("GET /package/status/track/{id}", packageTrackStatus(db, sessionManager))
	mux.HandleFunc("GET /package/status/check/{id}", packageCheckStatus(db, sessionManager))

	// notification channels page and corresponding routes for operations (add channel, delete channel, toggles, test, ack disabled by server)
	mux.HandleFunc("GET /channels", requireAuth(sessionManager, channelsPage(sessionManager, db, disp)))
	mux.HandleFunc("GET /channel/add/form", addChannelForm())
	mux.HandleFunc("GET /channel/add/cancel", addChannelFormCancel())
	mux.HandleFunc("POST /channel/add", addChannel(db, sessionManager))
	mux.HandleFunc("POST /channel/delete/{id}", deleteChannel(db, sessionManager))
	mux.HandleFunc("POST /channel/toggle/enabled/{id}", toggleChannelEnabled(db, sessionManager))
	mux.HandleFunc("POST /channel/toggle/manual/{id}", toggleNotifyOnManualVerify(db, sessionManager))
	mux.HandleFunc("POST /channel/test/{id}", testChannel(db, sessionManager, disp))
	mux.HandleFunc("POST /channel/ack-disabled/{id}", requireAuth(sessionManager, acknowledgeChannelDisabled(db, sessionManager, disp)))

	// notification delivery log page
	mux.HandleFunc("GET /log", requireAuth(sessionManager, notificationsPage(sessionManager, db, disp)))

	// admin system config
	mux.HandleFunc("GET /admin/config", requireAdmin(sessionManager, systemConfigPage(sessionManager, db, disp, chk)))
	mux.HandleFunc("POST /admin/config", requireAdmin(sessionManager, updateSystemConfig(db, disp, chk)))

	// admin profile management
	mux.HandleFunc("GET /admin/profiles", requireAdmin(sessionManager, profilesPage(sessionManager, db)))
	mux.HandleFunc("GET /admin/profiles/{id}/edit", requireAdmin(sessionManager, profileEditForm(db)))
	mux.HandleFunc("GET /admin/profiles/{id}/edit/cancel", requireAdmin(sessionManager, profileEditCancel(db)))
	mux.HandleFunc("POST /admin/profiles/{id}", requireAdmin(sessionManager, updateProfile(db)))

	// user profile menu - username update
	mux.HandleFunc("POST /profile/username", requireAuth(sessionManager, updateProfileUsername(sessionManager, db)))
}

// requireAuth redirects unauthenticated requests to /login.
func requireAuth(sessionManager *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionManager.GetUserID(r.Context()) == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireAdmin redirects unauthenticated requests to /login and rejects non-admin users with 403 Forbidden.
func requireAdmin(sessionManager *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionManager.GetUserID(r.Context()) == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if sessionManager.GetUserRole(r.Context()) != "admin" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
