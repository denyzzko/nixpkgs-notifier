package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/cleaner"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/ui/pages"
)

// getServerBaseURL reconstructs the server's base URL from the request.
// If TRUST_PROXY is enabled and X-Forwarded-Proto / X-Forwarded-Host headers are present,
// uses them unconditionally (the proxy is trusted to set them correctly).
// If TRUST_PROXY is disabled, forwarded headers are ignored entirely.
// Falls back to the incoming request host, and finally to cfg.ServerURL.
// Returns base URL without trailing slash (e.g., "https://example.com", "http://localhost:8080").
func getServerBaseURL(r *http.Request, cfg *config.Config) string {
	configuredURL := strings.TrimSuffix(cfg.ServerURL, "/")

	if cfg.TrustProxy {
		// Check for reverse proxy headers
		proto := r.Header.Get("X-Forwarded-Proto")
		host := r.Header.Get("X-Forwarded-Host")

		if proto != "" && host != "" {
			// Proxies may send multiple values; use the first hop.
			proto = strings.TrimSpace(strings.Split(proto, ",")[0])
			host = strings.TrimSpace(strings.Split(host, ",")[0])

			// Ensure host doesn't already have protocol
			host = strings.TrimPrefix(host, "http://")
			host = strings.TrimPrefix(host, "https://")
			return proto + "://" + host
		}
	}

	// Use direct request host when accessed without a proxy.
	if r.Host != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host := strings.TrimPrefix(r.Host, "http://")
		host = strings.TrimPrefix(host, "https://")
		return scheme + "://" + host
	}

	// Fallback to configured SERVER_URL
	return configuredURL
}

// renderHTML sets the Content-Type header to text/html and renders the given templ component.
func renderHTML(w http.ResponseWriter, ctx context.Context, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(ctx, w)
}

// parsePathID parses named path value as int64.
// Writes 400 response and returns false if value is missing or not valid.
func parsePathID(w http.ResponseWriter, r *http.Request, op string, name string) (int64, bool) {
	str := r.PathValue(name)
	if str == "" {
		writeGenericErr(w, op, "missing "+name, errors.New("missing path param "+name), http.StatusBadRequest)
		return 0, false
	}
	id, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		writeGenericErr(w, op, "invalid "+name, err, http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// parsePageQuery returns page number from the ?page= query parameter.
// Defaults to 1.
func parsePageQuery(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err == nil && p > 1 {
		return p
	}
	return 1
}

// buildPaginationURLs returns prev and next page URLs for the given base path.
func buildPaginationURLs(currentPage int, totalPages int, basePath string) (string, string) {
	var prev, next string
	if currentPage > 1 {
		prev = fmt.Sprintf("%s?page=%d", basePath, currentPage-1)
	}
	if currentPage < totalPages {
		next = fmt.Sprintf("%s?page=%d", basePath, currentPage+1)
	}
	return prev, next
}

// buildPackageRowVMs fetches active check states for the user and converts pkgPage items into PackageRowVMs.
func buildPackageRowVMs(ctx context.Context, db *database.Store, userID int64, pkgPage packages.AllPackagesPage) ([]pages.PackageRowVM, error) {
	checkStates, err := db.QueryCheckStatesByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	csMap := make(map[int64]*database.CheckState, len(checkStates))
	for i := range checkStates {
		csMap[checkStates[i].PackageID] = &checkStates[i]
	}
	items := make([]pages.PackageRowVM, 0, len(pkgPage.Items))
	for _, row := range pkgPage.Items {
		items = append(items, packageRowVM(row, csMap))
	}
	return items, nil
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

// runtimeConfigFromForm parses a RuntimeConfig from HTTP form.
// Returns an error if any field is missing or has an invalid value.
func runtimeConfigFromForm(r *http.Request) (config.RuntimeConfig, error) {
	// ensure all form values are populated
	if err := r.ParseForm(); err != nil {
		return config.RuntimeConfig{}, fmt.Errorf("invalid form data: %w", err)
	}

	// dispatcher fields
	dispatchInterval, err := parseDurationFromForm(r, "notification_dispatch_interval_val", "notification_dispatch_interval_unit")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	maxRetries, err := parseIntFromForm(r, "notification_max_retries")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	notifWorkers, err := parseIntFromForm(r, "notification_worker_count")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	if notifWorkers > 99 {
		return config.RuntimeConfig{}, fmt.Errorf("notification_worker_count must be between 1 and 99")
	}
	// checker fields
	checkInterval, err := parseDurationFromForm(r, "package_check_interval_val", "package_check_interval_unit")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	skipInterval, err := parseDurationFromForm(r, "package_check_skip_interval_val", "package_check_skip_interval_unit")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	checkWorkers, err := parseIntFromForm(r, "package_check_worker_count")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	if checkWorkers > 99 {
		return config.RuntimeConfig{}, fmt.Errorf("package_check_worker_count must be between 1 and 99")
	}
	// cleaner fields
	retentionDays, err := parseRetentionDaysFromForm(r)
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	// channel fields (user webhook and email limit)
	maxWebhooks, err := parseNonNegativeIntFromForm(r, "max_webhooks_per_user")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	maxEmails, err := parseNonNegativeIntFromForm(r, "max_emails_per_user")
	if err != nil {
		return config.RuntimeConfig{}, err
	}

	// return populated RuntimeConfig
	return config.RuntimeConfig{
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
		MaxEmailsPerUser:   maxEmails,
	}, nil
}
