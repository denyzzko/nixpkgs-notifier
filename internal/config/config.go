// Package config handles all application configuration (loading, validation and management).
//
// Configuration can be read from three sources:
//   - .env file in the working directory (optional - sugested for local dev)
//   - Environment variables injected directly into the process (suggested for production)
//   - Database overrides (persistent storage of UI config managed by admin - only checker and dispetcher parts of config are stored)
//
// Static config (server, database, OIDC, email) always comes from environment variables.
//
// Runtime config (dispatcher, checker) is first loaded from env,
// if admin previously saved some overrides from UI it is overriden with DB values.
// Handlers call GetRuntimeConfig to read current values and SaveRuntimeConfig to persist and apply changes.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/cleaner"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
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

// Config holds all configuration values.
// Static fields are always sourced from env variables.
// Runtime fields (Notification*, PackageCheck*) start from env defaults and can be overridden from database.
// Required variables are validated at startup (app will refuse to start if any are missing or invalid).
// Optional variables fall back to defaults.
type Config struct {
	ServerURL string
	// ServerPort is port the process binds to - may differ from the port in SERVER_URL when behind a reverse proxy
	ServerPort string // default: "8080" for TLSMode=off, "443" for TLSMode=on

	// TLS
	TLSMode     string // "off"/"on"
	TLSCertFile string // path to cert file (only for TLSMode=on)
	TLSKeyFile  string // path to key file (only for TLSMode=on)

	// TrustProxy controls whether X-Forwarded-Proto and X-Forwarded-Host headers
	// from a reverse proxy are trusted when reconstructing the server base URL.
	// Set true when running behind a reverse proxy (e.g. nginx), false when app is exposed directly to the internet.
	TrustProxy bool // default: false

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
	NotificationMaxRetries          int           // default: 3
	NotificationDisableOnMaxRetries bool          // default: true
	NotificationWorkerCount         int           // default: 2

	// Periodic package checker
	PackageCheckInterval     time.Duration // default: 12 hour
	PackageCheckWorkerCount  int           // default: 2
	PackageCheckSkipInterval time.Duration // default: 5 minutes

	// Notification cleaner
	NotificationRetentionDays int // default: 0 (disabled)

	// Notification Channels - webhook limit
	MaxWebhooksPerUser int // default: 0 (unlimited)
}

// RuntimeConfig holds the runtime settings managed by the admin via UI.
type RuntimeConfig struct {
	Dispatcher         dispatcher.Config
	Checker            checker.Config
	Cleaner            cleaner.Config
	MaxWebhooksPerUser int
}

// LoadEnvConfig loads configuration from environment variables.
// Reads .env file from the working directory first (.env file is not required, variables can be injected directly into the process)
// Returns an error if any required variable is missing or invalid.
func LoadEnvConfig() (*Config, error) {
	// load .env file from root of this project
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
			return nil, fmt.Errorf("config: OIDC_PROVIDERS is not valid JSON: %w", err)
		}
	}

	dispatchInterval := parseDurationFromEnv(os.Getenv("NOTIFICATION_DISPATCH_INTERVAL"), 5*time.Minute)
	maxRetries := parseIntFromEnv(os.Getenv("NOTIFICATION_MAX_RETRIES"), 3)
	disableOnMaxRetries := parseBoolFromEnv(os.Getenv("NOTIFICATION_DISABLE_ON_MAX_RETRIES"), true)
	workerCount := parseIntFromEnv(os.Getenv("NOTIFICATION_WORKER_COUNT"), 2)
	checkInterval := parseDurationFromEnv(os.Getenv("PACKAGE_CHECK_INTERVAL"), 12*time.Hour)
	checkWorkers := parseIntFromEnv(os.Getenv("PACKAGE_CHECK_WORKER_COUNT"), 2)
	checkSkipInterval := parseDurationFromEnv(os.Getenv("PACKAGE_CHECK_SKIP_THRESHOLD"), 5*time.Minute)

	trustProxy := parseBoolFromEnv(os.Getenv("TRUST_PROXY"), false)

	cfg := &Config{
		ServerURL:                       os.Getenv("SERVER_URL"),
		ServerPort:                      os.Getenv("SERVER_PORT"),
		TLSMode:                         os.Getenv("TLS_MODE"),
		TLSCertFile:                     os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:                      os.Getenv("TLS_KEY_FILE"),
		TrustProxy:                      trustProxy,
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

// LoadRuntimeOverrides tries to load settings from the database.
// If a saved config row exists it overwrites the env defaults on Config.
// If no row exists (admin has never saved config via UI, fresh system_config table) the env defaults are kept unchanged.
func (c *Config) LoadRuntimeOverrides(ctx context.Context, db *database.Store) {
	saved, err := db.QuerySystemConfig(ctx)
	if err != nil {
		// env defaults stay
		if errors.Is(err, database.ErrNotFound) {
			log.Println("[INFO] config: no database overrides found, using env defaults for dispatcher/checker")
		} else {
			log.Printf("[WARN] config: failed to load database overrides, using env defaults: %v", err)
		}
		return
	}

	log.Println("[INFO] config: loaded runtime overrides from database")
	c.NotificationDispatchInterval = saved.NotificationDispatchInterval
	c.NotificationMaxRetries = saved.NotificationMaxRetries
	c.NotificationDisableOnMaxRetries = saved.NotificationDisableOnMaxRetries
	c.NotificationWorkerCount = saved.NotificationWorkerCount
	c.PackageCheckInterval = saved.PackageCheckInterval
	c.PackageCheckWorkerCount = saved.PackageCheckWorkerCount
	c.PackageCheckSkipInterval = saved.PackageCheckSkipInterval
	c.NotificationRetentionDays = saved.NotificationRetentionDays
	c.MaxWebhooksPerUser = saved.MaxWebhooksPerUser
}

// parseDurationFromEnv parses a duration string (e.g. "5m", "12h").
// Returns fallback if the string is empty or cannot be parsed.
func parseDurationFromEnv(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// parseIntFromEnv parses a decimal integer string.
// Returns fallback if the string is empty or cannot be parsed.
func parseIntFromEnv(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// parseBoolFromEnv parses a boolean string ("true"/"false", "1"/"0", etc.).
// Returns fallback if the string is empty or cannot be parsed.
func parseBoolFromEnv(s string, fallback bool) bool {
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
func validateEnvConfig(cfg *Config) error {
	if cfg.ServerURL == "" {
		return fmt.Errorf("config: SERVER_URL is required")
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
			return fmt.Errorf("config: TLS_CERT_FILE is required when TLS_MODE=on")
		}
		if cfg.TLSKeyFile == "" {
			return fmt.Errorf("config: TLS_KEY_FILE is required when TLS_MODE=on")
		}
	default:
		return fmt.Errorf("config: TLS_MODE must be \"off\" or \"on\", got %q", cfg.TLSMode)
	}

	if cfg.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if (cfg.dbSSLMode == "verify-full" || cfg.dbSSLMode == "verify-ca") && cfg.DBSSLCACert == "" {
		return fmt.Errorf("config: DB_SSL_CA_CERT is required when DB_SSLMODE=%s", cfg.dbSSLMode)
	}

	// validate OIDC providers
	if len(cfg.OIDCProviders) == 0 {
		return fmt.Errorf("config: OIDC_PROVIDERS is required and must contain at least one provider")
	}
	seen := map[string]bool{}
	for i, p := range cfg.OIDCProviders {
		if p.Name == "" {
			return fmt.Errorf("config: OIDC_PROVIDERS[%d]: \"name\" is required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: OIDC_PROVIDERS: duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
		if p.Issuer == "" {
			return fmt.Errorf("config: OIDC_PROVIDERS[%d] (%q): \"issuer\" is required", i, p.Name)
		}
		if p.ClientID == "" {
			return fmt.Errorf("config: OIDC_PROVIDERS[%d] (%q): \"client_id\" is required", i, p.Name)
		}
		if p.ClientSecret == "" {
			return fmt.Errorf("config: OIDC_PROVIDERS[%d] (%q): \"client_secret\" is required", i, p.Name)
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
			return fmt.Errorf("config: RESEND_API_KEY is required when EMAIL_PROVIDER=resend")
		}
		if cfg.EmailFromAddr == "" {
			return fmt.Errorf("config: EMAIL_FROM_ADDR is required when EMAIL_PROVIDER=resend")
		}
	case "smtp":
		if cfg.SMTPHost == "" {
			return fmt.Errorf("config: SMTP_HOST is required when EMAIL_PROVIDER=smtp")
		}
		if cfg.SMTPPort == "" {
			return fmt.Errorf("config: SMTP_PORT is required when EMAIL_PROVIDER=smtp")
		}
		if cfg.SMTPFrom == "" {
			return fmt.Errorf("config: SMTP_FROM is required when EMAIL_PROVIDER=smtp")
		}
	default:
		return fmt.Errorf("config: EMAIL_PROVIDER must be \"resend\" or \"smtp\", got %q", cfg.EmailProvider)
	}

	return nil
}

// GetRuntimeConfig returns the current runtime config for display in the admin UI.
// First tries DB. Fallbacks to in-memory state of the running dispatcher and checker when no DB row exists yet.
func GetRuntimeConfig(ctx context.Context, db *database.Store, disp *dispatcher.Dispatcher, chk *checker.Checker, clnr *cleaner.Cleaner) RuntimeConfig {
	// try to load config from database
	saved, err := db.QuerySystemConfig(ctx)
	if err == nil {
		return RuntimeConfig{
			Dispatcher: dispatcher.Config{
				Interval:            saved.NotificationDispatchInterval,
				MaxRetries:          saved.NotificationMaxRetries,
				DisableOnMaxRetries: saved.NotificationDisableOnMaxRetries,
				WorkerCount:         saved.NotificationWorkerCount,
			},
			Checker: checker.Config{
				Interval:     saved.PackageCheckInterval,
				WorkerCount:  saved.PackageCheckWorkerCount,
				SkipInterval: saved.PackageCheckSkipInterval,
			},
			Cleaner: cleaner.Config{
				RetentionDays: saved.NotificationRetentionDays,
			},
			MaxWebhooksPerUser: saved.MaxWebhooksPerUser,
		}
	}
	// no DB row yet (or error happened during query) - reflect what dispatcher and checker currently run with
	return RuntimeConfig{
		Dispatcher: disp.GetConfig(),
		Checker:    chk.GetConfig(),
		Cleaner:    clnr.GetConfig(),
	}
}

// SaveRuntimeConfig stores rcfg to the database and immediately applies it to dispatcher and checker configs.
func SaveRuntimeConfig(ctx context.Context, db *database.Store, rcfg RuntimeConfig, disp *dispatcher.Dispatcher, chk *checker.Checker, clnr *cleaner.Cleaner) error {
	dbCfg := database.SystemConfig{
		NotificationDispatchInterval:    rcfg.Dispatcher.Interval,
		NotificationMaxRetries:          rcfg.Dispatcher.MaxRetries,
		NotificationDisableOnMaxRetries: rcfg.Dispatcher.DisableOnMaxRetries,
		NotificationWorkerCount:         rcfg.Dispatcher.WorkerCount,
		PackageCheckInterval:            rcfg.Checker.Interval,
		PackageCheckWorkerCount:         rcfg.Checker.WorkerCount,
		PackageCheckSkipInterval:        rcfg.Checker.SkipInterval,
		NotificationRetentionDays:       rcfg.Cleaner.RetentionDays,
		MaxWebhooksPerUser:              rcfg.MaxWebhooksPerUser,
	}
	// store config to database
	if err := db.UpdateSystemConfig(ctx, dbCfg); err != nil {
		return err
	}

	// apply to running workers
	disp.UpdateConfig(rcfg.Dispatcher)
	chk.UpdateConfig(rcfg.Checker)
	clnr.UpdateConfig(rcfg.Cleaner)

	return nil
}

// parseDurationFromForm parses a duration from two form fields: numeric value and a unit (seconds/minutes/hours).
// Returns an error if the value is missing, negative, or the unit is not recognised.
func parseDurationFromForm(r *http.Request, valField, unitField string) (time.Duration, error) {
	val, err := strconv.ParseFloat(r.FormValue(valField), 64)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid value for %s", valField)
	}
	switch r.FormValue(unitField) {
	case "seconds":
		return time.Duration(val * float64(time.Second)), nil
	case "minutes":
		return time.Duration(val * float64(time.Minute)), nil
	case "hours":
		return time.Duration(val * float64(time.Hour)), nil
	default:
		return 0, fmt.Errorf("invalid unit for %s", unitField)
	}
}

// parseIntFromForm parses a positive integer from form field.
// Returns an error if the value is missing, not a number, or less than 1.
func parseIntFromForm(r *http.Request, field string) (int, error) {
	v, err := strconv.Atoi(r.FormValue(field))
	if err != nil || v < 1 {
		return 0, fmt.Errorf("invalid value for %s", field)
	}
	return v, nil
}

// parseRetentionDaysFromForm parses the notification_retention_days select field.
// Valid values are 0 (disabled), 30, 90, 180 (these are days). Returns an error for any other value.
func parseRetentionDaysFromForm(r *http.Request) (int, error) {
	v, err := strconv.Atoi(r.FormValue("notification_retention_days"))
	if err != nil {
		return 0, fmt.Errorf("invalid value for notification_retention_days")
	}
	switch v {
	case 0, 30, 90, 180:
		return v, nil
	default:
		return 0, fmt.Errorf("invalid value for notification_retention_days: must be 0, 30, 90 or 180")
	}
}

// parseNonNegativeIntFromForm parses a non-negative integer from form field.
// Returns an error if the value is missing, not a number, or less then 0.
// Unlike parseIntFromForm 0 is valid value here (used for "unlimited" webhook limit setting).
func parseNonNegativeIntFromForm(r *http.Request, field string) (int, error) {
	v, err := strconv.Atoi(r.FormValue(field))
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid value for %s", field)
	}
	return v, nil
}

// RuntimeConfigFromForm parses a RuntimeConfig from HTTP form.
// Returns an error if any field is missing or has an invalid value.
func RuntimeConfigFromForm(r *http.Request) (RuntimeConfig, error) {
	// ensure all form values are populated
	if err := r.ParseForm(); err != nil {
		return RuntimeConfig{}, fmt.Errorf("invalid form data: %w", err)
	}

	// dispatcher fields
	dispatchInterval, err := parseDurationFromForm(r, "notification_dispatch_interval_val", "notification_dispatch_interval_unit")
	if err != nil {
		return RuntimeConfig{}, err
	}
	maxRetries, err := parseIntFromForm(r, "notification_max_retries")
	if err != nil {
		return RuntimeConfig{}, err
	}
	notifWorkers, err := parseIntFromForm(r, "notification_worker_count")
	if err != nil {
		return RuntimeConfig{}, err
	}
	// checker fields
	checkInterval, err := parseDurationFromForm(r, "package_check_interval_val", "package_check_interval_unit")
	if err != nil {
		return RuntimeConfig{}, err
	}
	skipInterval, err := parseDurationFromForm(r, "package_check_skip_interval_val", "package_check_skip_interval_unit")
	if err != nil {
		return RuntimeConfig{}, err
	}
	checkWorkers, err := parseIntFromForm(r, "package_check_worker_count")
	if err != nil {
		return RuntimeConfig{}, err
	}
	// cleaner fields
	retentionDays, err := parseRetentionDaysFromForm(r)
	if err != nil {
		return RuntimeConfig{}, err
	}
	// channel fields (user webhook limit)
	maxWebhooks, err := parseNonNegativeIntFromForm(r, "max_webhooks_per_user")
	if err != nil {
		return RuntimeConfig{}, err
	}

	// return populated RuntimeConfig
	return RuntimeConfig{
		Dispatcher: dispatcher.Config{
			Interval:            dispatchInterval,
			MaxRetries:          maxRetries,
			DisableOnMaxRetries: r.FormValue("notification_disable_on_max_retries") == "on",
			WorkerCount:         notifWorkers,
		},
		Checker: checker.Config{
			Interval:     checkInterval,
			WorkerCount:  checkWorkers,
			SkipInterval: skipInterval,
		},
		Cleaner: cleaner.Config{
			RetentionDays: retentionDays,
		},
		MaxWebhooksPerUser: maxWebhooks,
	}, nil
}
