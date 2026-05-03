// Package web contains HTTP layer of the application.
//
// It is organised in four files:
//   - router.go:      registers all routes and access control wrappers (requireAuth, requireAdmin)
//   - handlers.go:    HTTP handler functions
//   - viewmodels.go:  converts database and app types to template view models (e.g. ChannelVM)
//   - webErrors.go:   maps appError causes to HTTP status codes and writes error responses (writeGenericErr for plain errors)
package web

import (
	"net/http"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/cleaner"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/time/rate"
)

// RegisterRoutes registers all HTTP routes on mux.
// Each handler receives only the dependencies it needs.
func RegisterRoutes(mux *http.ServeMux, cfg *config.Config, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher, chk *checker.Checker, clnr *cleaner.Cleaner) {
	// ip rate limiter for unauthenticated auth endpoints
	ipLimit := middleware.RateLimitIP(rate.Limit(10.0/60), 5) // 10 req/min, burst 5

	// user rate limiter for authenticated write endpoints that trigger expensive operations
	userLimit := middleware.RateLimitUser(rate.Limit(20.0/60), 10, sessionManager) // 20 req/min, burst 10

	// home page (displays all tracked packages)
	mux.HandleFunc("GET /", requireAuth(sessionManager, indexPage(sessionManager, db)))

	// login and logout
	mux.HandleFunc("GET /login", loginPage(provMap, sessionManager))
	mux.HandleFunc("GET /auth/login", ipLimit(login(cfg, provMap, sessionManager)))
	mux.HandleFunc("GET /auth/callback", ipLimit(callback(db, provMap, sessionManager)))
	mux.HandleFunc("POST /auth/logout", requireAuth(sessionManager, logout(sessionManager)))

	// routes for package operations (package verifications, track/untrack, watchlist)
	mux.HandleFunc("POST /package/check/{id}", requireAuthLimited(sessionManager, userLimit, checkTrackedPackage(db, sessionManager, chk)))
	mux.HandleFunc("POST /packages/check-all", requireAuthLimited(sessionManager, userLimit, checkAllPackages(db, sessionManager, chk)))
	mux.HandleFunc("POST /package/untrack/{id}", requireAuth(sessionManager, untrackPackage(db, sessionManager)))
	mux.HandleFunc("GET /package/track/form", requireAuth(sessionManager, trackPackageForm()))
	mux.HandleFunc("GET /package/track/cancel", requireAuth(sessionManager, trackPackageFormCancel()))
	mux.HandleFunc("POST /package/track", requireAuthLimited(sessionManager, userLimit, trackPackage(db, sessionManager, chk)))
	mux.HandleFunc("GET /package/status/track/{id}", requireAuth(sessionManager, packageTrackStatus(db, sessionManager)))
	mux.HandleFunc("GET /package/status/check/{id}", requireAuth(sessionManager, packageCheckStatus(db, sessionManager)))
	mux.HandleFunc("POST /package/watch", requireAuthLimited(sessionManager, userLimit, watchPackage(db, sessionManager)))
	mux.HandleFunc("POST /package/unwatch/{id}", requireAuth(sessionManager, unwatchPackage(db, sessionManager)))
	mux.HandleFunc("POST /package/watch/check/{id}", requireAuthLimited(sessionManager, userLimit, checkWatchedPackage(db, sessionManager, chk)))
	mux.HandleFunc("GET /package/watch/status/check/{id}", requireAuth(sessionManager, watchCheckStatus(db, sessionManager)))

	// notification channels page and corresponding routes for operations (add channel, delete channel, toggles, test, ack disabled by server)
	mux.HandleFunc("GET /channels", requireAuth(sessionManager, channelsPage(sessionManager, db, disp, cfg)))
	mux.HandleFunc("GET /channel/add/form", requireAuth(sessionManager, addChannelForm()))
	mux.HandleFunc("GET /channel/add/cancel", requireAuth(sessionManager, addChannelFormCancel()))
	mux.HandleFunc("POST /channel/add", requireAuthLimited(sessionManager, userLimit, addChannel(db, sessionManager, cfg)))
	mux.HandleFunc("POST /channel/delete/{id}", requireAuth(sessionManager, deleteChannel(db, sessionManager)))
	mux.HandleFunc("POST /channel/toggle/enabled/{id}", requireAuth(sessionManager, toggleChannelEnabled(db, sessionManager)))
	mux.HandleFunc("POST /channel/toggle/manual/{id}", requireAuth(sessionManager, toggleNotifyOnManualVerify(db, sessionManager)))
	mux.HandleFunc("POST /channel/test/{id}", requireAuthLimited(sessionManager, userLimit, testChannel(db, sessionManager, disp)))
	mux.HandleFunc("POST /channel/ack-disabled/{id}", requireAuth(sessionManager, acknowledgeChannelDisabled(db, sessionManager, disp)))

	// notification delivery log page
	mux.HandleFunc("GET /log", requireAuth(sessionManager, notificationsPage(sessionManager, db, disp)))

	// admin system config
	mux.HandleFunc("GET /admin/config", requireAdmin(sessionManager, systemConfigPage(sessionManager, db, disp, chk, clnr)))
	mux.HandleFunc("POST /admin/config", requireAdmin(sessionManager, updateSystemConfig(db, disp, chk, clnr, cfg)))

	// admin profile management
	mux.HandleFunc("GET /admin/profiles", requireAdmin(sessionManager, profilesPage(sessionManager, db)))
	mux.HandleFunc("GET /admin/profiles/{id}/edit", requireAdmin(sessionManager, profileEditForm(db)))
	mux.HandleFunc("GET /admin/profiles/{id}/edit/cancel", requireAdmin(sessionManager, profileEditCancel(db)))
	mux.HandleFunc("POST /admin/profiles/{id}", requireAdmin(sessionManager, updateProfile(db)))

	// user profile menu - username update
	mux.HandleFunc("POST /profile/username", requireAuthLimited(sessionManager, userLimit, updateProfileUsername(sessionManager, db)))

	// account linking
	mux.HandleFunc("GET /accounts", requireAuth(sessionManager, accountsPage(db, sessionManager)))
	mux.HandleFunc("GET /auth/link", requireAuth(sessionManager, linkAccount(provMap, sessionManager)))
	mux.HandleFunc("POST /account/unlink", requireAuthLimited(sessionManager, userLimit, unlinkAccount(db, sessionManager)))
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

// requireAdmin rejects requests from unauthenticated or non-admin users.
func requireAdmin(sessionManager *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(sessionManager, func(w http.ResponseWriter, r *http.Request) {
		if sessionManager.GetUserRole(r.Context()) != "admin" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// requireAuthLimited is like requireAuth but also applies per-user rate limit.
func requireAuthLimited(sm *session.SessionManager, limit func(http.HandlerFunc) http.HandlerFunc, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(sm, limit(next))
}
