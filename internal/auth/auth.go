package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/env"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/oauth2"
)

type ProviderMap struct {
	Providers map[string]*Provider
}

type Provider struct {
	Name     string
	Issuer   string
	Verifier *oidc.IDTokenVerifier
	Config   *oauth2.Config
}

type ProviderConfig struct {
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

type OIDCSecrets struct {
	State         string
	Nonce         string
	CodeVerifier  string
	CodeChallenge string
}

type UserClaims struct {
	Subject           string
	Email             string
	EmailVerified     bool
	PreferredUsername string
}

func SetupProviders(ctx context.Context, cfg *env.EnvConfig) (*ProviderMap, error) {
	provMap := &ProviderMap{Providers: map[string]*Provider{}}

	provCfgs := setupProvidersConfigs(cfg)

	for _, pCfg := range provCfgs {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 48-62)
		provider, err := oidc.NewProvider(ctx, pCfg.Issuer)
		if err != nil {
			return nil, err
		}
		oidcConfig := &oidc.Config{
			ClientID: pCfg.ClientID,
		}
		verifier := provider.Verifier(oidcConfig)

		config := oauth2.Config{
			ClientID:     pCfg.ClientID,
			ClientSecret: pCfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  pCfg.RedirectURL,
			Scopes:       pCfg.Scopes,
		}

		provMap.Providers[pCfg.Name] = &Provider{
			Name:     pCfg.Name,
			Issuer:   pCfg.Issuer,
			Verifier: verifier,
			Config:   &config,
		}
	}

	return provMap, nil

}

func setupProvidersConfigs(cfg *env.EnvConfig) []ProviderConfig {
	provCfgs := []ProviderConfig{
		{
			Name:         "google",
			Issuer:       "https://accounts.google.com",
			ClientID:     cfg.ClientIDGoogle,
			ClientSecret: cfg.ClientSecretGoogle,
			RedirectURL:  cfg.ServerURL + "/auth/callback",
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		/*
			{
				Name:			"apple",
				Issuer:			"https://xx.apple.com",
				ClientID:		cfg.ClientIDApple,
				ClientSecret: 	cfg.ClientSecretApple,
				RedirectURL: 	cfg.ServerURL + "/auth/callback",
				Scopes: 		[]string{oidc.ScopeOpenID, "email", "profile"},
			},
			...
		*/
	}

	return provCfgs
}

// Random base64url string, used for state/nonce/verifier generation
func randString(nByte int) (string, error) {
	b := make([]byte, nByte)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCE S256 transform: base64url(SHA256(code_verifier))
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return challenge
}

// Generate secrets (state, nonce, code verifier, challenge) that are used during OIDC auth
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

// Generates the OIDC authorization URL with PKCE parameters (URL that user is redirected to for authentication)
func buildAuthURL(provider *Provider, secrets OIDCSecrets) string {
	return provider.Config.AuthCodeURL(
		secrets.State,
		oidc.Nonce(secrets.Nonce),
		oauth2.SetAuthURLParam("code_challenge", secrets.CodeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

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

func verifyAndExtractClaims(ctx context.Context, provider *Provider, rawIDToken string, expectedNonce string) (UserClaims, error) {
	var claims UserClaims

	// Verify ID token
	idToken, err := provider.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return claims, fmt.Errorf("failed to verify ID token: %w", err)
	}

	// Verify nonce to prevent replay attacks
	if idToken.Nonce != expectedNonce {
		return claims, fmt.Errorf("nonce mismatch: got %q, expected %q", idToken.Nonce, expectedNonce)
	}

	// Extract claims from ID token
	var rawClaims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
	}

	if err := idToken.Claims(&rawClaims); err != nil {
		return claims, fmt.Errorf("failed to parse claims: %w", err)
	}

	claims.Subject = rawClaims.Sub
	claims.Email = rawClaims.Email
	claims.EmailVerified = rawClaims.EmailVerified
	claims.PreferredUsername = rawClaims.PreferredUsername

	return claims, nil
}

func extractIDToken(token *oauth2.Token) (string, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("no id_token field in oauth2 token")
	}
	return rawIDToken, nil
}

func AuthCodeFlowInitLogin(ctx context.Context, sessionManager *session.SessionManager, provider *Provider, providerName string) (string, error) {
	// generate secrets (random state, nonce, code verifier and challenge)
	secrets, err := createOIDCSecrets()
	if err != nil {
		return "", fmt.Errorf("internal error while creating secrets: %w", err)
	}

	// store necessary oidc secrets in session for later verification
	sessionManager.SaveOIDCSecrets(ctx, secrets.State, session.OIDCAuthData{
		Nonce:        secrets.Nonce,
		CodeVerifier: secrets.CodeVerifier,
		Provider:     providerName,
	})

	// build authorization url
	authURL := buildAuthURL(provider, secrets)

	return authURL, nil
}

func AuthCodeFlowCallback(ctx context.Context, sessionManager *session.SessionManager, provMap *ProviderMap, state string, code string) (UserClaims, *Provider, error) {
	// pop oidc data from session (verifies also match of state)
	oidcData, ok := sessionManager.PopOIDCSecrets(ctx, state)
	if !ok {
		return UserClaims{}, nil, fmt.Errorf("unknown/expired state")
	}

	// get provider by name
	provider, ok := provMap.Providers[oidcData.Provider]
	if !ok {
		return UserClaims{}, nil, fmt.Errorf("unknown provider %q", oidcData.Provider)
	}

	// exchange authorization code for tokens
	oauth2Token, err := exchangeCodeForToken(ctx, provider, code, oidcData.CodeVerifier)
	if err != nil {
		return UserClaims{}, nil, fmt.Errorf("oidc exchange code: %w", err)
	}

	// extract ID token from OAuth2 response
	rawIDToken, err := extractIDToken(oauth2Token)
	if err != nil {
		return UserClaims{}, nil, fmt.Errorf("oidc extract id_token: %w", err)
	}

	// verify ID token and extract user claims
	claims, err := verifyAndExtractClaims(ctx, provider, rawIDToken, oidcData.Nonce)
	if err != nil {
		return UserClaims{}, nil, fmt.Errorf("oidc verify/claims: %w", err)
	}

	// renew session token
	err = sessionManager.RenewToken(ctx)
	if err != nil {
		return UserClaims{}, nil, fmt.Errorf("error renewing token: %w", err)
	}

	return claims, provider, nil
}
