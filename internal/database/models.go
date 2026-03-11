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

// An enabled channel belonging to a user (with email or webhook details)
// used when sending notifications
type ActiveChannel struct {
	ChannelID            int64
	UserID               int64
	EmailAddress         *string
	WebhookURL           *string
	NotifyOnManualVerify bool
}

// Channel that user has configured
type UserChannel struct {
	ID                   int64
	IsEnabled            bool
	EmailAddress         *string
	WebhookURL           *string
	NotifyOnManualVerify *bool
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
	EmailAddress  *string
	WebhookURL    *string
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
	EmailAddress  *string
	WebhookURL    *string
}
