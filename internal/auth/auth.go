package auth

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/env"
	"golang.org/x/oauth2"
)

type ProviderMap struct {
	Providers map[string]*Provider
}

type Provider struct {
	Name     string
	URL      string
	Verifier *oidc.IDTokenVerifier
	Config   *oauth2.Config
}

type ProviderConfig struct {
	Name         string
	URL          string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

func SetupProviders(ctx context.Context, cfg *env.EnvConfig) (*ProviderMap, error) {
	provMap := &ProviderMap{Providers: map[string]*Provider{}}

	provCfgs := setupProvidersConfigs(cfg)

	for _, pCfg := range provCfgs {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 48-62)
		provider, err := oidc.NewProvider(ctx, pCfg.URL)
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
			URL:      pCfg.URL,
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
			URL:          "https://accounts.google.com",
			ClientID:     cfg.ClientIDGoogle,
			ClientSecret: cfg.ClientSecretGoogle,
			RedirectURL:  cfg.ServerURL + "/auth/callback",
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		/*
			{
				Name:			"apple",
				URL:			"https://xx.apple.com",
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
