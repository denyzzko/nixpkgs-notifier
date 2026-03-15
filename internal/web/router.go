package web

import (
	"net/http"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

func RegisterRoutes(mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager, disp *dispatcher.Dispatcher, chk *checker.Checker) {
	// home page (displays all tracked packages)
	mux.HandleFunc("GET /", requireAuth(sessionManager, indexPage(sessionManager, db)))

	// login page and corresponding routes
	mux.HandleFunc("GET /login", loginPage(sessionManager))
	mux.HandleFunc("GET /auth/login", login(provMap, sessionManager))
	mux.HandleFunc("GET /auth/callback", callback(db, provMap, sessionManager))
	mux.HandleFunc("POST /auth/logout", logout(sessionManager))

	// routes for package operations (package verifications, track/untrack)
	mux.HandleFunc("POST /package/check/{id}", checkTrackedPackage(db, sessionManager, chk))
	mux.HandleFunc("POST /package/check/all", checkAllTrackedPackages(db, sessionManager, chk))
	mux.HandleFunc("POST /package/untrack/{id}", untrackPackage(db, sessionManager))
	mux.HandleFunc("GET /package/track/form", trackPackageForm())
	mux.HandleFunc("GET /package/track/cancel", trackPackageFormCancel())
	mux.HandleFunc("POST /package/track", trackPackage(db, sessionManager, chk))

	// notification channels page and corresponding routes for operations (add channel, delete channel, toggles, test)
	mux.HandleFunc("GET /channels", requireAuth(sessionManager, channelsPage(sessionManager, db)))
	mux.HandleFunc("GET /channel/add/form", addChannelForm())
	mux.HandleFunc("GET /channel/add/cancel", addChannelFormCancel())
	mux.HandleFunc("POST /channel/add", addChannel(db, sessionManager))
	mux.HandleFunc("POST /channel/delete/{id}", deleteChannel(db, sessionManager))
	mux.HandleFunc("POST /channel/toggle/enabled/{id}", toggleChannelEnabled(db, sessionManager))
	mux.HandleFunc("POST /channel/toggle/manual/{id}", toggleNotifyOnManualVerify(db, sessionManager))
	mux.HandleFunc("POST /channel/test/{id}", testChannel(db, sessionManager, disp))

	// notification delivery log page
	mux.HandleFunc("GET /log", requireAuth(sessionManager, notificationsPage(sessionManager, db, disp)))
}

func requireAuth(sessionManager *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionManager.GetUserID(r.Context()) == 0 {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}
