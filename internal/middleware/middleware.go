package middleware

import (
	"log"
	"net/http"
	"strings"

	"github.com/denyzzko/nixpkgs-notifier/internal/config"
)

type Middleware func(http.Handler) http.Handler

func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

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
