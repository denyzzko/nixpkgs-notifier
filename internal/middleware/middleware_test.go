package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/time/rate"
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
// -------------------- RateLimitIP -------------------------------
// ----------------------------------------------------------------

func TestRateLimitIP_AllowsRequestsWithinBurst(t *testing.T) {
	limit := middleware.RateLimitIP(rate.Limit(10), 3) // burst of 3
	handler := limit(okHandler)

	for i := range 3 {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d (OK)", i+1, rr.Code, http.StatusOK)
		}
	}
}

func TestRateLimitIP_BlocksRequestsExceedingBurst(t *testing.T) {
	limit := middleware.RateLimitIP(rate.Limit(0), 1) // rate=0 no refill, burst of 1
	handler := limit(okHandler)

	// first request consumes the burst
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:1234"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// second request should be rejected
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "5.6.7.8:1234"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d (TooManyRequests)", rr2.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimitIP_XForwardedForTakesPriorityOverRemoteAddr(t *testing.T) {
	// two requests, same X-Forwarded-For IP but different RemoteAddr
	// they should share the same limiter
	limit := middleware.RateLimitIP(rate.Limit(0), 1) // burst of 1, no refill
	handler := limit(okHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("X-Forwarded-For", "9.9.9.9")
	req1.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// same forwarded IP, different RemoteAddr should be rate limited
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", "9.9.9.9")
	req2.RemoteAddr = "10.0.0.2:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req2)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d (TooManyRequests)", rr.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimitIP_DifferentIPsHaveSeparateLimiters(t *testing.T) {
	limit := middleware.RateLimitIP(rate.Limit(0), 1) // burst of 1, no refill
	handler := limit(okHandler)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "11.11.11.11:1234"
	handler.ServeHTTP(httptest.NewRecorder(), req1)
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// different IP should not be limited
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "22.22.22.22:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req2)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (OK) - different IPs should not share limiters", rr.Code, http.StatusOK)
	}
}

// ----------------------------------------------------------------
// ------------------- RateLimitUser ------------------------------
// ----------------------------------------------------------------

func TestRateLimitUser_AllowsRequestsWithinBurst(t *testing.T) {
	sm := newSession()
	limit := middleware.RateLimitUser(rate.Limit(10), 3, sm) // burst of 3
	handler := limit(okHandler)

	for i := range 3 {
		req := requestWithSession(t, sm, 1, "user")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d (OK)", i+1, rr.Code, http.StatusOK)
		}
	}
}

func TestRateLimitUser_BlocksRequestsExceedingBurst(t *testing.T) {
	sm := newSession()
	limit := middleware.RateLimitUser(rate.Limit(0), 1, sm) // burst of 1, no refill
	handler := limit(okHandler)

	// consume the burst
	req := requestWithSession(t, sm, 42, "user")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// next request from same user should be blocked
	req2 := requestWithSession(t, sm, 42, "user")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req2)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d (TooManyRequests)", rr.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimitUser_DifferentUsersHaveSeparateLimiters(t *testing.T) {
	sm := newSession()
	limit := middleware.RateLimitUser(rate.Limit(0), 1, sm) // burst of 1, no refill
	handler := limit(okHandler)

	// exhaust limiter for user 1
	req1 := requestWithSession(t, sm, 100, "user")
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// user 2 should have their own fresh limiter
	req2 := requestWithSession(t, sm, 200, "user")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (OK) - different users should not share limiters", rr2.Code, http.StatusOK)
	}
}

func TestRateLimitUser_UnauthenticatedPassesThrough(t *testing.T) {
	sm := newSession()
	limit := middleware.RateLimitUser(rate.Limit(0), 0, sm) // zero burst - would block anyone with a userID
	handler := limit(okHandler)

	// no userID in session - should pass through without rate limiting
	req := requestWithSession(t, sm, 0, "")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (OK) - unauthenticated requests bypass user rate limiter", rr.Code, http.StatusOK)
	}
}
