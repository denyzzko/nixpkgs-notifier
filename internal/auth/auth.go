package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/env"
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
func CreateOIDCSecrets() (OIDCSecrets, error) {
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
