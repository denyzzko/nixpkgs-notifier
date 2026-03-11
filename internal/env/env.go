package env

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type EnvConfig struct {
	ServerURL   string
	DatabaseURL string

	// OIDC
	ClientIDGoogle     string
	ClientSecretGoogle string
	//ClientIDApple		string
	//ClientSecretApple	string
	//...

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

func LoadEnvConfig() (*EnvConfig, error) {

	// load .env file from root of this project
	err := godotenv.Load()
	if err != nil {
		return nil, err
	}

	// build db url
	dbUrl := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASS")),
		Host:     net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT")),
		Path:     os.Getenv("DB_NAME"),
		RawQuery: "sslmode=" + url.QueryEscape(os.Getenv("DB_SSLMODE")),
	}

	dispatchInterval := parseDuration(os.Getenv("NOTIFICATION_DISPATCH_INTERVAL"), 5*time.Minute)
	maxRetries := parseInt(os.Getenv("NOTIFICATION_MAX_RETRIES"), 3)
	disableOnMaxRetries := parseBool(os.Getenv("NOTIFICATION_DISABLE_ON_MAX_RETRIES"), true)
	workerCount := parseInt(os.Getenv("NOTIFICATION_WORKER_COUNT"), 2)
	checkInterval := parseDuration(os.Getenv("PACKAGE_CHECK_INTERVAL"), 12*time.Hour)
	checkWorkers := parseInt(os.Getenv("PACKAGE_CHECK_WORKER_COUNT"), 2)
	checkSkipInterval := parseDuration(os.Getenv("PACKAGE_CHECK_SKIP_THRESHOLD"), 5*time.Minute)

	cfg := &EnvConfig{
		ServerURL:          os.Getenv("SERVER_URL"),
		DatabaseURL:        dbUrl.String(),
		ClientIDGoogle:     os.Getenv("GOOGLE_OAUTH2_CLIENT_ID"),
		ClientSecretGoogle: os.Getenv("GOOGLE_OAUTH2_CLIENT_SECRET"),
		//clientIDApple:	 os.Getenv("APPLE_OAUTH2_CLIENT_ID"),
		//clientSecretApple: os.Getenv("APPLE_OAUTH2_CLIENT_SECRET"),
		//...
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

func parseBool(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return defaultVal
	}
	return b
}

// Checks that all required environment variables are filled
func validateEnvConfig(cfg *EnvConfig) error {
	// required for all configs
	if cfg.ServerURL == "" {
		return fmt.Errorf("env: SERVER_URL is required")
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("env: DATABASE_URL is required")
	}
	if cfg.ClientIDGoogle == "" {
		return fmt.Errorf("env: GOOGLE_OAUTH2_CLIENT_ID is required")
	}
	if cfg.ClientSecretGoogle == "" {
		return fmt.Errorf("env: GOOGLE_OAUTH2_CLIENT_SECRET is required")
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
