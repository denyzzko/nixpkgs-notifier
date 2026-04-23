package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/web"
)

// okHandler just writes 200 OK.
// Used to verify that middleware passes request through.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// newSession creates a real session manager (with in-memory store).
// Used to inject userID and role into request context via LoadAndSave middleware.
func newSession() *session.SessionManager {
	return session.NewManager(false)
}

// requestWithSession creates test request with initialized session context.
func requestWithSession(t *testing.T, sm *session.SessionManager, userID int64, role string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	// run through LoadAndSave to get properly initialised session context
	sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID != 0 {
			sm.Put(r.Context(), "userID", userID)
			sm.PutUserRole(r.Context(), role)
		}
		req = r // request with populated session context
	})).ServeHTTP(rr, req)

	return req
}

// ----------------------------------------------------------------
// ---------------------- requireAuth -----------------------------
// ----------------------------------------------------------------

func TestRequireAuth_UnauthenticatedRedirectsToLogin(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 0, "") // no userID in session

	rr := httptest.NewRecorder()
	web.ExportRequireAuth(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d (Found)", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestRequireAuth_AuthenticatedPassesThrough(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 1, "user")

	rr := httptest.NewRecorder()
	web.ExportRequireAuth(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (OK)", rr.Code, http.StatusOK)
	}
}

// ----------------------------------------------------------------
// --------------------- requireAdmin -----------------------------
// ----------------------------------------------------------------

func TestRequireAdmin_UnauthenticatedRedirectsToLogin(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 0, "")

	rr := httptest.NewRecorder()
	web.ExportRequireAdmin(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d (Found)", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestRequireAdmin_AuthenticatedNonAdminForbidden(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 1, "user")

	rr := httptest.NewRecorder()
	web.ExportRequireAdmin(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (Forbidden)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireAdmin_AuthenticatedEmptyRoleForbidden(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 1, "") // role not set

	rr := httptest.NewRecorder()
	web.ExportRequireAdmin(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (Forbidden)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireAdmin_AdminPassesThrough(t *testing.T) {
	sm := newSession()
	req := requestWithSession(t, sm, 1, "admin")

	rr := httptest.NewRecorder()
	web.ExportRequireAdmin(sm, okHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (OK)", rr.Code, http.StatusOK)
	}
}
