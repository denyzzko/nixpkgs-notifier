// Package auth implements OIDC authentication using the Authorization Code Flow with PKCE.
//
// At startup, SetupProviders initializes one Provider per entry in the config by fetching
// provider's discovery document and building OAuth2 client config.
// At login, AuthCodeFlowInitLogin generates authorization URL and stores
// the OIDC secrets (state, nonce, code verifier) in the session.
// At callback, AuthCodeFlowCallback verifies state, exchanges code for tokens,
// verifies the ID token and nonce, and returns user's claims.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/env"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/oauth2"
)

// ProviderMap holds all configured OIDC providers keyed by their name.
// Initialized once at startup by SetupProviders and then treated as read-only.
type ProviderMap struct {
	Providers map[string]*Provider
}

// Provider holds the runtime state for a single configured OIDC provider.
type Provider struct {
	Name        string
	DisplayName string
	Issuer      string
	Verifier    *oidc.IDTokenVerifier // used to validate ID tokens issued by this provider
	Config      *oauth2.Config        // OAuth2 client config used to build auth URLs and exchange codes
}

// ProviderConfig is struct used during provider initialization.
// It is built from env.OIDCProviderConfig and passed to SetupProviders.
type ProviderConfig struct {
	Name         string
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// OIDCSecrets holds the one-time values generated at start of each login attempt.
// CodeVerifier and CodeChallenge implement PKCE (S256 method).
type OIDCSecrets struct {
	State         string // ties the callback to this specific login request and prevents CSRF
	Nonce         string // embedded in the ID token and verified on callback to prevent replay attacks
	CodeVerifier  string
	CodeChallenge string
}

// UserClaims holds the identity claims extracted from verified ID token.
type UserClaims struct {
	Subject           string
	Email             string
	EmailVerified     bool
	PreferredUsername string // may be empty if provider does not include this claim (e.g. Google does not support it)
}

// SetupProviders initializes a ProviderMap from the given config.
// For each configured provider it fetches OIDC discovery document,
// builds a token verifier and an OAuth2 client config.
// Returns an error if any provider's discovery document cannot be fetched.
func SetupProviders(ctx context.Context, cfg *env.EnvConfig) (*ProviderMap, error) {
	provMap := &ProviderMap{Providers: map[string]*Provider{}}

	provCfgs := setupProvidersConfigs(cfg)

	for _, pCfg := range provCfgs {
		// fetch provider metadata from OIDC discovery endpoint and build ID token verifier
		// block of code adopted from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 48-62)
		provider, err := oidc.NewProvider(ctx, pCfg.Issuer)
		if err != nil {
			return nil, err
		}

		// configure verifier to check that tokens are issued for our client ID
		oidcConfig := &oidc.Config{
			ClientID: pCfg.ClientID,
		}
		verifier := provider.Verifier(oidcConfig)

		// build OAuth2 client config using endpoints from the discovery document
		config := oauth2.Config{
			ClientID:     pCfg.ClientID,
			ClientSecret: pCfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  pCfg.RedirectURL,
			Scopes:       pCfg.Scopes,
		}

		provMap.Providers[pCfg.Name] = &Provider{
			Name:        pCfg.Name,
			DisplayName: pCfg.DisplayName,
			Issuer:      pCfg.Issuer,
			Verifier:    verifier,
			Config:      &config,
		}
	}

	return provMap, nil
}

// setupProvidersConfigs converts raw env config entries into ProviderConfig structs,
// appending the server's callback URL as redirect URL for each provider.
func setupProvidersConfigs(cfg *env.EnvConfig) []ProviderConfig {
	provCfgs := make([]ProviderConfig, 0, len(cfg.OIDCProviders))
	for _, p := range cfg.OIDCProviders {
		provCfgs = append(provCfgs, ProviderConfig{
			Name:         p.Name,
			DisplayName:  p.DisplayName,
			Issuer:       p.Issuer,
			ClientID:     p.ClientID,
			ClientSecret: p.ClientSecret,
			RedirectURL:  cfg.ServerURL + "/auth/callback",
			Scopes:       p.Scopes,
		})
	}
	return provCfgs
}

// randString returns a cryptographically random base64url-encoded string of nByte random bytes.
// Used to generate state, nonce, and PKCE code verifier values.
func randString(nByte int) (string, error) {
	b := make([]byte, nByte)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceS256 computes the PKCE S256 code challenge: base64url(SHA256(code_verifier)).
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return challenge
}

// createOIDCSecrets generates a fresh set of one-time OIDC secrets for a login attempt.
// All values are cryptographically random.
// Code challenge is derived from the verifier using S256 method.
func createOIDCSecrets() (OIDCSecrets, error) {
	var secrets OIDCSecrets
	state, err := randString(16)
	if err != nil {
		return secrets, err
	}
	nonce, err := randString(16)
	if err != nil {
		return secrets, err
	}
	verifier, err := randString(32)
	if err != nil {
		return secrets, err
	}
	challenge := pkceS256(verifier)

	secrets = OIDCSecrets{
		State:         state,
		Nonce:         nonce,
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
	}

	return secrets, nil
}

// buildAuthURL constructs the authorization URL user is redirected to for login.
// Includes state (CSRF protection), nonce (replay protection), and PKCE S256 parameters.
func buildAuthURL(provider *Provider, secrets OIDCSecrets) string {
	return provider.Config.AuthCodeURL(
		secrets.State,
		oidc.Nonce(secrets.Nonce),
		oauth2.SetAuthURLParam("code_challenge", secrets.CodeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// exchangeCodeForToken exchanges authorization code returned by the provider
// for an OAuth2 token set, sending the PKCE code verifier to prove ownership.
func exchangeCodeForToken(ctx context.Context, provider *Provider, code string, codeVerifier string) (*oauth2.Token, error) {
	token, err := provider.Config.Exchange(
		ctx,
		code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange token: %w", err)
	}

	return token, nil
}

// verifyAndExtractClaims verifies the ID token's signature and nonce and extracts user identity claims from it.
// Returns an error if the token is invalid, expired, or nonce does not match.
func verifyAndExtractClaims(ctx context.Context, provider *Provider, rawIDToken string, expectedNonce string) (UserClaims, error) {
	var claims UserClaims

	// verify ID token (signature, expiry and audience against provider public keys)
	idToken, err := provider.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return claims, fmt.Errorf("failed to verify ID token: %w", err)
	}

	// verify nonce to prevent replay attacks
	if idToken.Nonce != expectedNonce {
		return claims, fmt.Errorf("nonce mismatch: got %q, expected %q", idToken.Nonce, expectedNonce)
	}

	// extract claims from verified ID token
	var rawClaims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
	}

	// unmarshal the token payload into rawClaims
	err = idToken.Claims(&rawClaims)
	if err != nil {
		return claims, fmt.Errorf("failed to parse claims: %w", err)
	}

	claims.Subject = rawClaims.Sub
	claims.Email = rawClaims.Email
	claims.EmailVerified = rawClaims.EmailVerified
	claims.PreferredUsername = rawClaims.PreferredUsername

	return claims, nil
}

// extractIDToken pulls the raw ID token string out of OAuth2 token response.
// Returns an error if the id_token field is missing.
func extractIDToken(token *oauth2.Token) (string, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("no id_token field in oauth2 token")
	}
	return rawIDToken, nil
}

// AuthCodeFlowInitLogin starts OIDC Authorization Code Flow for the given provider.
// It generates fresh OIDC secrets, stores them in session under the state key,
// and returns authorization URL user should be redirected to.
func AuthCodeFlowInitLogin(ctx context.Context, sessionManager *session.SessionManager, provider *Provider, providerName string) (string, error) {
	const op = "auth.AuthCodeFlowInitLogin"

	// generate one-time secrets for this login attempt
	secrets, err := createOIDCSecrets()
	if err != nil {
		return "", appError.NewAppError(op, appError.Internal, "internal error", err)
	}

	// store nonce, code verifier and provider name in session keyed by state
	// (state is sent to provider and echoed back on callback, used to retrieve these values)
	sessionManager.SaveOIDCSecrets(ctx, secrets.State, session.OIDCAuthData{
		Nonce:        secrets.Nonce,
		CodeVerifier: secrets.CodeVerifier,
		Provider:     providerName,
	})

	// build authorization URL
	authURL := buildAuthURL(provider, secrets)

	return authURL, nil
}

// AuthCodeFlowCallback completes the OIDC Authorization Code Flow after
// provider redirects user back to /auth/callback with a code and state.
// It verifies the state, exchanges code for tokens, verifies the ID token and nonce,
// renews the session token to prevent session fixation, and returns the user's claims.
func AuthCodeFlowCallback(ctx context.Context, sessionManager *session.SessionManager, provMap *ProviderMap, state string, code string) (UserClaims, *Provider, error) {
	const op = "auth.AuthCodeFlowCallback"

	// pop OIDC secrets stored at login initiation, verifying state matches
	oidcData, ok := sessionManager.PopOIDCSecrets(ctx, state)
	if !ok {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Invalid, "invalid or expired login state", fmt.Errorf("unknown/expired state in OIDC flow (on callback), state: '%q'", state))
	}

	// look up provider that initiated this login
	provider, ok := provMap.Providers[oidcData.Provider]
	if !ok {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Invalid, "unknown identity provider", fmt.Errorf("unknown provider %q in oidc session", oidcData.Provider))
	}

	// exchange authorization code for tokens
	oauth2Token, err := exchangeCodeForToken(ctx, provider, code, oidcData.CodeVerifier)
	if err != nil {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Upstream, "failed to complete login with provider", fmt.Errorf("failed to exchange code for tokens in OIDC flow (on callback): %w", err))
	}

	// extract raw ID token string from OAuth2 response
	rawIDToken, err := extractIDToken(oauth2Token)
	if err != nil {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Invalid, "invalid login response", fmt.Errorf("failed to extract id token from response in OIDC flow (on callback): %w", err))
	}

	// verify ID token and extract user claims
	claims, err := verifyAndExtractClaims(ctx, provider, rawIDToken, oidcData.Nonce)
	if err != nil {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Invalid, "invalid login response", fmt.Errorf("failed to verify/extract claims in OIDC flow (on callback): %w", err))
	}

	// renew session token to prevent session fixation after successful login
	err = sessionManager.RenewToken(ctx)
	if err != nil {
		return UserClaims{}, nil, appError.NewAppError(op, appError.Internal, "internal error", fmt.Errorf("failed to renew session: %w", err))
	}

	return claims, provider, nil
}
