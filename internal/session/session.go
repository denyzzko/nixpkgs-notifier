package session

import (
	"context"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

// SessionManager wraps the scs session manager and exposes only methods needed by this application.
type SessionManager struct {
	manager *scs.SessionManager
}

// OIDCAuthData holds the temporary secrets stored in session during OIDC authorization code flow.
// They are written on login initiation and popped on callback to prevent replay.
type OIDCAuthData struct {
	Nonce        string
	CodeVerifier string
	Provider     string
}

// NewManager creates and configures a new SessionManager.
// secureCookie should be true whenever the app is served over HTTPS.
func NewManager(secureCookie bool) *SessionManager {
	var sessionManager SessionManager

	// register custom type with gob
	gob.Register(OIDCAuthData{})

	sm := scs.New()
	sm.Lifetime = 24 * time.Hour
	sm.IdleTimeout = 30 * time.Minute
	sm.Cookie.Secure = secureCookie

	sessionManager.manager = sm
	return &sessionManager
}

// LoadAndSave is HTTP middleware that loads the session for each request and saves any changes at the end.
func (sm *SessionManager) LoadAndSave(next http.Handler) http.Handler {
	return sm.manager.LoadAndSave(next)
}

// SaveOIDCSecrets stores OIDC flow secrets in the session keyed by state.
// The state value is later used to retrieve and verify them on callback.
func (sm *SessionManager) SaveOIDCSecrets(ctx context.Context, key string, value OIDCAuthData) {
	sm.manager.Put(ctx, "oidc:"+key, value)
}

// PopOIDCSecrets retrieves and removes OIDC flow secrets from the session.
// Returns false if no data exists for the given state (expired or invalid).
func (sm *SessionManager) PopOIDCSecrets(ctx context.Context, key string) (OIDCAuthData, bool) {
	value := sm.manager.Pop(ctx, "oidc:"+key)
	data, ok := value.(OIDCAuthData)
	return data, ok
}

// RenewToken regenerates the session token after a successful login to prevent session fixation attacks.
func (sm *SessionManager) RenewToken(ctx context.Context) error {
	return sm.manager.RenewToken(ctx)
}

// Put stores an arbitrary value in the session under the given key.
func (sm *SessionManager) Put(ctx context.Context, key string, value any) {
	sm.manager.Put(ctx, key, value)
}

// Get retrieves a value from the session by key (nil if not found).
func (sm *SessionManager) Get(ctx context.Context, key string) any {
	value := sm.manager.Get(ctx, key)
	return value
}

// GetUserID returns authenticated user's ID from the session.
// Returns 0 if no user is logged in.
func (sm *SessionManager) GetUserID(ctx context.Context) int64 {
	return sm.manager.GetInt64(ctx, "userID")
}

// Destroy deletes the entire session (used on logout).
func (sm *SessionManager) Destroy(ctx context.Context) error {
	return sm.manager.Destroy(ctx)
}
