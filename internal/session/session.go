// Package session wraps the scs session manager and exposes session
// operations needed by this application.
//
// It handles storage for authenticated user identity, temporary OIDC flow secrets
// and account linking context.
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

// LinkData holds context about in-progress account linking operation.
type LinkData struct {
	Mode   string // new/existing
	UserID int64  // user who initiated linking
}

// NewManager creates and configures a new SessionManager.
// secureCookie should be true whenever the app is served over HTTPS.
func NewManager(secureCookie bool) *SessionManager {
	var sessionManager SessionManager

	// register custom types with gob
	gob.Register(OIDCAuthData{})
	gob.Register(LinkData{})

	sm := scs.New()
	sm.Lifetime = 24 * time.Hour
	sm.IdleTimeout = 30 * time.Minute
	sm.Cookie.Secure = secureCookie
	sm.Cookie.SameSite = http.SameSiteLaxMode

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

// PutUserRole stores role of authenticated user in session.
func (sm *SessionManager) PutUserRole(ctx context.Context, role string) {
	sm.manager.Put(ctx, "userRole", role)
}

// GetUserRole returns role of authenticated user from session (empty string if not set).
func (sm *SessionManager) GetUserRole(ctx context.Context) string {
	return sm.manager.GetString(ctx, "userRole")
}

// PutUsername stores username of authenticated user in session.
func (sm *SessionManager) PutUsername(ctx context.Context, username string) {
	sm.manager.Put(ctx, "username", username)
}

// GetUsername returns username of authenticated user from session (empty string if not set).
func (sm *SessionManager) GetUsername(ctx context.Context) string {
	return sm.manager.GetString(ctx, "username")
}

// SaveLinkData stores account linking context in session.
func (sm *SessionManager) SaveLinkData(ctx context.Context, data LinkData) {
	sm.manager.Put(ctx, "linkData", data)
}

// PopLinkData retrieves and removes account linking context from the session.
// Returns false if no link flow is in progress (normal login callback).
func (sm *SessionManager) PopLinkData(ctx context.Context) (LinkData, bool) {
	value := sm.manager.Pop(ctx, "linkData")
	data, ok := value.(LinkData)
	return data, ok
}

// Destroy deletes the entire session (used on logout).
func (sm *SessionManager) Destroy(ctx context.Context) error {
	return sm.manager.Destroy(ctx)
}
