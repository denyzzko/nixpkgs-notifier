// Package database provides the data access layer for application.
//
// It is organised in these files:
//   - database.go:              opens connection pool and runs migrations
//   - embeds.go:                embeds all SQL files into the binary at compile time
//   - models.go:                defines data types returned by queries
//   - queries_channels.go:      notification channel operations
//   - queries_check_state.go:   check state operations (pending/done/failed/not_found rows written by check goroutines and read by polling endpoints)
//   - queries_config.go:        system configuration operations
//   - queries_helpers.go:       shared helpers and sentinel errors used across query files
//   - queries_notifications.go: notification creation and delivery operations
//   - queries_packages.go:      package  operations
//   - queries_trackings.go:      tracking operations
//   - queries_users.go:         user and account operations
//   - queries_watchlist.go:     watchlist operations
package database

import "time"

// Internal user in the system
type User struct {
	ID        int64
	CreatedAt time.Time
	Username  string
	Role      string
}

// External identity linked to internal User
type Account struct {
	UserID        int64
	CreatedAt     time.Time
	Provider      string
	Issuer        string
	Subject       string
	Email         *string
	EmailVerified bool
}

// Used when creating user account
type UserInfo struct {
	Email         *string
	EmailVerified bool
	Username      *string
	Role          string
	Provider      string
	Issuer        string
	Subject       string
}

// Nixpkgs package in specific branch
type Package struct {
	ID             int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastCheckedAt  *time.Time
	Name           string
	Branch         string
	CurrentVersion string
}

// User's tracking of a specific Package
type Tracking struct {
	CreatedAt           time.Time
	UpdatedAt           time.Time
	UserID              int64
	PackageID           int64
	LastNotifiedVersion string
}

// Combines users's tracking and the package
type TrackedPackage struct {
	PackageID           int64
	Name                string
	Branch              string
	LastNotifiedVersion string
	LastCheckedAt       *time.Time // last time nix eval was evaluated for that package
	CurrentVersion      string     // current version of package
}

// Email channel details
type Email struct {
	Address string
}

// Webhook channel details
type Webhook struct {
	URL        string
	Type       string // "generic" or "mattermost"
	Username   string // mattermost only (empty string for generic)
	Channel    string // mattermost only (empty string for generic)
	Priority   string // mattermost only (empty string for generic)
	RequestAck bool   // mattermost only (false string for generic)
}

// An enabled channel belonging to a user (with email or webhook details)
// used when sending notifications
type ActiveChannel struct {
	ChannelID            int64
	UserID               int64
	Email                *Email   // nil for webhook
	Webhook              *Webhook // nil for email
	NotifyOnManualVerify bool
}

// Channel that user has configured
type UserChannel struct {
	ID                   int64
	IsEnabled            bool
	DisabledByServer     bool
	Email                *Email   // nil for webhook
	Webhook              *Webhook // nil for email
	NotifyOnManualVerify bool
}

// Pairs active channel and version that is specific for that user package tracking (used when creating notifications)
type ChannelNotification struct {
	Channel    ActiveChannel
	OldVersion string
}

type UserNotification struct {
	ID            int64
	DetectedAt    time.Time
	OldVersion    string
	NewVersion    string
	Status        NotificationStatus
	AttemptCount  int
	ErrorMessage  *string
	PackageName   string
	PackageBranch string
	Email         *Email   // nil for webhook
	Webhook       *Webhook // nil for email
}

// Status of notification
type NotificationStatus string

const (
	NotificationStatusPending NotificationStatus = "pending"
	NotificationStatusSent    NotificationStatus = "sent"
	NotificationStatusFailed  NotificationStatus = "failed"
)

// A notification that is to be sent (with email or webhook data)
type PendingFailedNotification struct {
	ID            int64
	ChannelID     int64
	PackageID     int64
	PackageName   string
	PackageBranch string
	OldVersion    string
	NewVersion    string
	DetectedAt    time.Time
	AttemptCount  int
	UserID        int64
	Email         *Email   // nil for webhook
	Webhook       *Webhook // nil for email
}

// SystemConfig holds admin-configurable (from UI) runtime settings for notification dispatcher and package checker.
type SystemConfig struct {
	NotificationDispatchInterval    time.Duration
	NotificationMaxRetries          int
	NotificationDisableOnMaxRetries bool
	NotificationWorkerCount         int
	PackageCheckInterval            time.Duration
	PackageCheckWorkerCount         int
	PackageCheckSkipInterval        time.Duration
	NotificationRetentionDays       int
	MaxWebhooksPerUser              int
	MaxEmailsPerUser                int
}

// WatchlistEntry is package that user watches for future appearance in Nixpkgs.
type WatchlistEntry struct {
	ID        int64
	CreatedAt time.Time
	UserID    int64
	PackageID int64
}

// WatchedPackage combines watchlist entry with its package details.
// Non-existing packages have current_version = "".
type WatchedPackage struct {
	WatchlistID int64
	CreatedAt   time.Time
	UserID      int64
	PackageID   int64
	Name        string
	Branch      string
}

// DistinctWatchlistEntry represents a unique (package_id, name, branch) from the watchlist.
// Used by background scheduler.
type DistinctWatchlistEntry struct {
	PackageID int64
	Name      string
	Branch    string
}

// CheckState holds the persisted result of a check for one (user, package) pair.
// Used for both tracked and watched packages.
// Expires after 1 hour.
type CheckState struct {
	UserID     int64
	PackageID  int64
	Status     string  // "pending", "done", "failed", "not_found"
	OldVersion *string // nil for watched packages (no version yet)
	NewVersion *string // non-nil when done and version changed
	ErrorMsg   *string
	StartedAt  time.Time
	ExpiresAt  time.Time
}

// PackageRow is result row returned by the paginated packages query.
type PackageRow struct {
	Kind                string // "tracked" or "watching"
	PackageID           int64
	Name                string
	Branch              string
	LastNotifiedVersion *string    // non-nil for tracked packages only
	LastCheckedAt       *time.Time // nil if the package was never checked
	CurrentVersion      string
	WatchlistID         *int64 // non-nil for watched packages only
}
