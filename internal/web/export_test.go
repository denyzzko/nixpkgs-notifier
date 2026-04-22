package web

// This file is only compiled during tests.
// It exposes private middleware functions so the external test package can test them.

import (
	"net/http"

	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

func ExportRequireAuth(sm *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(sm, next)
}

func ExportRequireAdmin(sm *session.SessionManager, next http.HandlerFunc) http.HandlerFunc {
	return requireAdmin(sm, next)
}
