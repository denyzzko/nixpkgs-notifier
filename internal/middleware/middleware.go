// Package middleware provides HTTP handler wrappers
// such as logging, security headers, and rate limiting.
package middleware

import (
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/time/rate"
)

// ipLimiters stores one rate.Limiter per client IP.
var ipLimiters sync.Map

// userLimiters stores one rate.Limiter per authenticated user ID.
var userLimiters sync.Map

// Middleware is function that wraps http.Handler to add behaviour before or after it.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple middlewares into one, applying them in the order they are given.
// The first middleware in the list is the outermost wrapper (runs first on way in).
func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// RequestLogger logs HTTP method and path of every incoming request.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("got request: %s %s", r.Method, r.URL.Path)

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders returns a middleware that sets standard HTTP security headers.
// cfg is used to conditionally set HSTS (only when serving over HTTPS).
func SecurityHeaders(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Prevent browsers from sniffing the content type
			h.Set("X-Content-Type-Options", "nosniff")
			// Deny framing entirely to prevent clickjacking
			h.Set("X-Frame-Options", "DENY")
			// Restrict referrer information sent to other origins
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			// Only allow resources from same origin; unsafe-inline required for HTMX and templ
			h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data: https://cdn.simpleicons.org; font-src 'self' https://cdn.jsdelivr.net")
			// Disable legacy IE XSS filter (modern browsers ignore this; 0 is the safe value)
			h.Set("X-XSS-Protection", "0")
			// Deny access to all browser features the app does not use
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			// Suppress the Go server version banner
			h.Set("Server", "")
			// HSTS: only set when serving over HTTPS - sending this on HTTP can lock users out permanently
			if strings.HasPrefix(cfg.ServerURL, "https://") {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// realIP extracts client IP from the request.
// Checks X-Forwarded-For first (set by reverse proxies), fallsback to RemoteAddr.
func realIP(r *http.Request) string {
	// X-Forwarded-For can contain multiple IPs (comma-separated) when passing through multiple proxies
	// leftmost entry is the original client IP
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		idx := strings.Index(xff, ",")
		if idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// RemoteAddr is "host:port" format
	// strip the port to get the bare IP
	idx := strings.LastIndex(r.RemoteAddr, ":")
	if idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

// RateLimitIP returns handler wrapper that applies per-IP token bucket rate limit.
// Intended for unauthenticated endpoints such as /auth/login and /auth/callback.
// Requests exceeding the limit receive HTTP 429 (Too Many Requests).
func RateLimitIP(r rate.Limit, burst int) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			ip := realIP(req)
			// LoadOrStore atomically returns existing limiter for this IP if it exists,
			// or stores and returns a newly created one
			l, _ := ipLimiters.LoadOrStore(ip, rate.NewLimiter(r, burst))
			if !l.(*rate.Limiter).Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next(w, req)
		}
	}
}

// RateLimitUser returns handler wrapper that applies per-user token bucket rate limit.
// Intended for authenticated write endpoints such as /package/track and /channel/add.
// If no user is logged in the request passes through (requireAuth rejects it before this is applied).
// Requests exceeding the limit receive HTTP 429 (Too Many Requests).
func RateLimitUser(r rate.Limit, burst int, sm *session.SessionManager) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			userID := sm.GetUserID(req.Context())
			if userID != 0 {
				// LoadOrStore atomically returns existing limiter for this user if it exists,
				// or stores and returns a newly created one
				l, _ := userLimiters.LoadOrStore(userID, rate.NewLimiter(r, burst))
				if !l.(*rate.Limiter).Allow() {
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
					return
				}
			}
			next(w, req)
		}
	}
}
