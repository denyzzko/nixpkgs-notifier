package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/cleaner"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// systemConfigPage renders the admin system configuration page with current runtime values.
func systemConfigPage(sessionManager *session.SessionManager, db *database.Store, disp *dispatcher.Dispatcher, chk *checker.Checker, clnr *cleaner.Cleaner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get current runtime config
		rc := config.GetRuntimeConfig(ctx, db, disp, chk, clnr)

		// render response
		vm := systemConfigVM(rc.Dispatcher, rc.Checker, rc.Cleaner, rc.MaxWebhooksPerUser, rc.MaxEmailsPerUser)
		vm.Saved = r.URL.Query().Get("saved") == "1"
		vm.BaseVM = buildBaseVM(ctx, r, db, sessionManager)
		renderHTML(w, ctx, pages.SystemConfigPage(vm))
	}
}

// updateSystemConfig handles POST from the admin system config form.
// Validates, persists, and applies the new runtime configuration.
func updateSystemConfig(db *database.Store, disp *dispatcher.Dispatcher, chk *checker.Checker, clnr *cleaner.Cleaner, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// parse and validate runtime config from the submitted form
		rcfg, err := runtimeConfigFromForm(r)
		if err != nil {
			writeGenericErr(w, "web.updateSystemConfig", err.Error(), err, http.StatusBadRequest)
			return
		}

		// store config to database and apply to dispatcher, checker and cleaner
		err = config.SaveRuntimeConfig(ctx, db, rcfg, disp, chk, clnr)
		if err != nil {
			writeAppErr(w, "web.updateSystemConfig", err)
			return
		}

		// update in-memory config so new webhook and email limit takes effect immediately without server restart
		cfg.MaxWebhooksPerUser = rcfg.MaxWebhooksPerUser
		cfg.MaxEmailsPerUser = rcfg.MaxEmailsPerUser

		http.Redirect(w, r, "/admin/config?saved=1", http.StatusSeeOther)
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
			BaseVM:   buildBaseVM(ctx, r, db, sessionManager),
			Profiles: make([]pages.ProfileVM, 0, len(usrs)),
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

		// extract user ID from request
		id, ok := parsePathID(w, r, "web.profileEditForm", "id")
		if !ok {
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

		// extract user ID from request
		id, ok := parsePathID(w, r, "web.profileEditCancel", "id")
		if !ok {
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

		// extract user ID from request
		id, ok := parsePathID(w, r, "web.updateProfile", "id")
		if !ok {
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
