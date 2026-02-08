package session

import (
	"context"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

type SessionManager struct {
	manager *scs.SessionManager
}

type OIDCAuthData struct {
	Nonce        string
	CodeVerifier string
	Provider     string
}

func NewManager() *SessionManager {
	var sessionManager SessionManager

	// register custom type with gob
	gob.Register(OIDCAuthData{})

	sm := scs.New()
	sm.Lifetime = 24 * time.Hour
	sm.IdleTimeout = 30 * time.Minute
	//m.Cookie.Secure = true

	sessionManager.manager = sm
	return &sessionManager
}

func (sm *SessionManager) LoadAndSave(next http.Handler) http.Handler {
	return sm.manager.LoadAndSave(next)
}

func (sm *SessionManager) SaveOIDCSecrets(ctx context.Context, key string, value OIDCAuthData) {
	sm.manager.Put(ctx, "oidc:"+key, value)
}

func (sm *SessionManager) PopOIDCSecrets(ctx context.Context, key string) (OIDCAuthData, bool) {
	value := sm.manager.Pop(ctx, "oidc:"+key)
	data, ok := value.(OIDCAuthData)
	return data, ok
}

func (sm *SessionManager) RenewToken(ctx context.Context) error {
	return sm.manager.RenewToken(ctx)
}

func (sm *SessionManager) Put(ctx context.Context, key string, value any) {
	sm.manager.Put(ctx, key, value)
}

func (sm *SessionManager) Get(ctx context.Context, key string) any {
	value := sm.manager.Get(ctx, key)
	return value
}

func (sm *SessionManager) GetUserID(ctx context.Context) int64 {
	return sm.manager.GetInt64(ctx, "userID")
}

func (sm *SessionManager) Destroy(ctx context.Context) error {
	return sm.manager.Destroy(ctx)
}
