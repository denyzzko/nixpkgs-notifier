// Package env handles loading and validation of application configuration from environment variables.
//
// Configuration can be read from two sources:
//   - Optional .env file in the working directory (local dev)
//   - Environment variables injected directly into the process (production)
//
// All required variables are validated at startup via validateEnvConfig.
// Application will refuse to start if any required field is missing or invalid.
package env

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// OIDCProviderConfig holds the configuration for a single OIDC identity provider.
// Defined as entries in the OIDC_PROVIDERS JSON array environment variable.
type OIDCProviderConfig struct {
	// Name is a short unique identifier used in URLs, e.g. "google", "authentik".
	// Must be URL-safe (no spaces or special characters).
	Name string `json:"name"`
	// DisplayName is human-readable label shown on the login button, e.g. "Google", "School SSO".
	// Falls back to Name if empty.
	DisplayName string `json:"display_name"`
	// Issuer is the OIDC discovery URL, e.g. "https://accounts.google.com".
	Issuer string `json:"issuer"`
	// ClientID is OAuth2 client ID registered with the provider.
	ClientID string `json:"client_id"`
	// ClientSecret is OAuth2 client secret registered with the provider.
	ClientSecret string `json:"client_secret"`
	// Scopes is list of OAuth2 scopes to request.
	// Defaults to ["openid", "email", "profile"] if empty.
	Scopes []string `json:"scopes"`
}

// EnvConfig holds all configuration values loaded from environment variables.
// Required variables are validated at startup via validateEnvConfig.
// Optional variables fall back to defaults.
type EnvConfig struct {
	ServerURL string
	// ServerPortis port the process binds to - may differ from the port in SERVER_URL when behind a reverse proxy
	ServerPort string // default: "8080" for TLSMode=off, "443" for TLSMode=on

	// TLS
	TLSMode     string // "off"/"on"
	TLSCertFile string // path to cert file (only for TLSMode=on)
	TLSKeyFile  string // path to key file (only for TLSMode=on)

	DatabaseURL string
	// Database TLS
	dbSSLMode   string // "disable"/"require"/"verify-full"/"verify-ca"
	DBSSLCACert string // optional path to CA cert for sslmode=verify-full

	// OIDC - one entry per identity provider, parsed from OIDC_PROVIDERS JSON
	OIDCProviders []OIDCProviderConfig

	// SMTP Email
	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	// Resend Email
	ResendAPIKey  string
	EmailFromAddr string

	// Which provider to use ("resend" or "smtp")
	EmailProvider string

	// Notification dispatcher
	NotificationDispatchInterval    time.Duration // default: 5 minutes
	NotificationMaxRetries          int           // defualt: 3
	NotificationDisableOnMaxRetries bool          // default: true
	NotificationWorkerCount         int           // default: 2

	// Periodic package checker
	PackageCheckInterval     time.Duration // default: 12 hour
	PackageCheckWorkerCount  int           // default: 2
	PackageCheckSkipInterval time.Duration // default: 5 minutes
}

// LoadEnvConfig loads configuration from environment variables.
// Reads .env file from the working directory first (.env file is not required, variables can be injected directly into the process)
// Returns an error if any required variable is missing or invalid.
func LoadEnvConfig() (*EnvConfig, error) {
	// load .env file from root of this project
	// optional - env vars may be injected directly
	err := godotenv.Load()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// build db url
	dbSSLMode := os.Getenv("DB_SSLMODE")
	dbQuery := "sslmode=" + url.QueryEscape(dbSSLMode)
	caCert := os.Getenv("DB_SSL_CA_CERT")
	if caCert != "" {
		dbQuery += "&sslrootcert=" + url.QueryEscape(caCert)
	}
	dbUrl := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASS")),
		Host:     net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT")),
		Path:     os.Getenv("DB_NAME"),
		RawQuery: dbQuery,
	}

	// parse OIDC providers from JSON
	var oidcProviders []OIDCProviderConfig
	if raw := os.Getenv("OIDC_PROVIDERS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &oidcProviders); err != nil {
			return nil, fmt.Errorf("env: OIDC_PROVIDERS is not valid JSON: %w", err)
		}
	}

	dispatchInterval := parseDuration(os.Getenv("NOTIFICATION_DISPATCH_INTERVAL"), 5*time.Minute)
	maxRetries := parseInt(os.Getenv("NOTIFICATION_MAX_RETRIES"), 3)
	disableOnMaxRetries := parseBool(os.Getenv("NOTIFICATION_DISABLE_ON_MAX_RETRIES"), true)
	workerCount := parseInt(os.Getenv("NOTIFICATION_WORKER_COUNT"), 2)
	checkInterval := parseDuration(os.Getenv("PACKAGE_CHECK_INTERVAL"), 12*time.Hour)
	checkWorkers := parseInt(os.Getenv("PACKAGE_CHECK_WORKER_COUNT"), 2)
	checkSkipInterval := parseDuration(os.Getenv("PACKAGE_CHECK_SKIP_THRESHOLD"), 5*time.Minute)

	cfg := &EnvConfig{
		ServerURL:                       os.Getenv("SERVER_URL"),
		ServerPort:                      os.Getenv("SERVER_PORT"),
		TLSMode:                         os.Getenv("TLS_MODE"),
		TLSCertFile:                     os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:                      os.Getenv("TLS_KEY_FILE"),
		DatabaseURL:                     dbUrl.String(),
		dbSSLMode:                       dbSSLMode,
		DBSSLCACert:                     os.Getenv("DB_SSL_CA_CERT"),
		OIDCProviders:                   oidcProviders,
		EmailProvider:                   os.Getenv("EMAIL_PROVIDER"),
		SMTPHost:                        os.Getenv("SMTP_HOST"),
		SMTPPort:                        os.Getenv("SMTP_PORT"),
		SMTPUser:                        os.Getenv("SMTP_USER"),
		SMTPPass:                        os.Getenv("SMTP_PASS"),
		SMTPFrom:                        os.Getenv("SMTP_FROM"),
		ResendAPIKey:                    os.Getenv("RESEND_API_KEY"),
		EmailFromAddr:                   os.Getenv("EMAIL_FROM_ADDR"),
		NotificationDispatchInterval:    dispatchInterval,
		NotificationMaxRetries:          maxRetries,
		NotificationDisableOnMaxRetries: disableOnMaxRetries,
		NotificationWorkerCount:         workerCount,
		PackageCheckInterval:            checkInterval,
		PackageCheckWorkerCount:         checkWorkers,
		PackageCheckSkipInterval:        checkSkipInterval,
	}

	// validate all required fields are filled
	err = validateEnvConfig(cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseDuration parses a duration string (e.g. "5m", "12h").
// Returns fallback if the string is empty or cannot be parsed.
func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// parseInt parses a decimal integer string.
// Returns fallback if the string is empty or cannot be parsed.
func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// parseBool parses a boolean string ("true"/"false", "1"/"0", etc.).
// Returns fallback if the string is empty or cannot be parsed.
func parseBool(s string, fallback bool) bool {
	if s == "" {
		return fallback
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return fallback
	}
	return b
}

// validateEnvConfig checks that all required fields are correctly set.
// It also fills in default ports that were left out.
func validateEnvConfig(cfg *EnvConfig) error {
	if cfg.ServerURL == "" {
		return fmt.Errorf("env: SERVER_URL is required")
	}

	if cfg.TLSMode == "" {
		// TLS_MODE defaults to off if not set
		cfg.TLSMode = "off"
	}

	switch cfg.TLSMode {
	case "off":
		if cfg.ServerPort == "" {
			cfg.ServerPort = "8080"
		}
	case "on":
		if cfg.ServerPort == "" {
			cfg.ServerPort = "443"
		}
		if cfg.TLSCertFile == "" {
			return fmt.Errorf("env: TLS_CERT_FILE is required when TLS_MODE=on")
		}
		if cfg.TLSKeyFile == "" {
			return fmt.Errorf("env: TLS_KEY_FILE is required when TLS_MODE=on")
		}
	default:
		return fmt.Errorf("env: TLS_MODE must be \"off\" or \"on\", got %q", cfg.TLSMode)
	}

	if cfg.DatabaseURL == "" {
		return fmt.Errorf("env: DATABASE_URL is required")
	}
	if (cfg.dbSSLMode == "verify-full" || cfg.dbSSLMode == "verify-ca") && cfg.DBSSLCACert == "" {
		return fmt.Errorf("env: DB_SSL_CA_CERT is required when DB_SSLMODE=%s", cfg.dbSSLMode)
	}

	// validate OIDC providers
	if len(cfg.OIDCProviders) == 0 {
		return fmt.Errorf("env: OIDC_PROVIDERS is required and must contain at least one provider")
	}
	seen := map[string]bool{}
	for i, p := range cfg.OIDCProviders {
		if p.Name == "" {
			return fmt.Errorf("env: OIDC_PROVIDERS[%d]: \"name\" is required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("env: OIDC_PROVIDERS: duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
		if p.Issuer == "" {
			return fmt.Errorf("env: OIDC_PROVIDERS[%d] (%q): \"issuer\" is required", i, p.Name)
		}
		if p.ClientID == "" {
			return fmt.Errorf("env: OIDC_PROVIDERS[%d] (%q): \"client_id\" is required", i, p.Name)
		}
		if p.ClientSecret == "" {
			return fmt.Errorf("env: OIDC_PROVIDERS[%d] (%q): \"client_secret\" is required", i, p.Name)
		}
		// default scopes if not specified
		if len(p.Scopes) == 0 {
			cfg.OIDCProviders[i].Scopes = []string{"openid", "email", "profile"}
		}
		// default display name to name if not specified
		if p.DisplayName == "" {
			cfg.OIDCProviders[i].DisplayName = p.Name
		}
	}

	// validate email provider and its required fields
	switch cfg.EmailProvider {
	case "resend":
		if cfg.ResendAPIKey == "" {
			return fmt.Errorf("env: RESEND_API_KEY is required when EMAIL_PROVIDER=resend")
		}
		if cfg.EmailFromAddr == "" {
			return fmt.Errorf("env: EMAIL_FROM_ADDR is required when EMAIL_PROVIDER=resend")
		}
	case "smtp":
		if cfg.SMTPHost == "" {
			return fmt.Errorf("env: SMTP_HOST is required when EMAIL_PROVIDER=smtp")
		}
		if cfg.SMTPPort == "" {
			return fmt.Errorf("env: SMTP_PORT is required when EMAIL_PROVIDER=smtp")
		}
		if cfg.SMTPFrom == "" {
			return fmt.Errorf("env: SMTP_FROM is required when EMAIL_PROVIDER=smtp")
		}
	default:
		return fmt.Errorf("env: EMAIL_PROVIDER must be \"resend\" or \"smtp\", got %q", cfg.EmailProvider)
	}

	return nil
}
