package database

import "time"

// Internal user in the system
type User struct {
	ID        int64
	CreatedAt time.Time
	Username  *string
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

// Nixpkgs package in specific branch
type Package struct {
	ID             int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

// Notification channel configured by a user
type Channel struct {
	ID        int64
	CreatedAt time.Time
	UpdatedAt time.Time
	UserID    int64
	IsEnabled bool
}

// Email notification Channel
type Email struct {
	ChannelID   int64
	EmailAdress string
}

// Webhook notification Channel
type Webhook struct {
	ChannelID  int64
	WebhookURL string
}

// Status of notification
type NotificationStatus string

const (
	NotificationStatusPending NotificationStatus = "pending"
	NotificationStatusSent    NotificationStatus = "sent"
	NotificationStatusFailed  NotificationStatus = "failed"
)

// Notification record
type Notification struct {
	ID           int64
	CreatedAt    time.Time
	ChannelID    int64
	PackageID    int64
	DetectedAt   time.Time
	OldVersion   string
	NewVersion   string
	Status       NotificationStatus
	AttempCount  int
	ErrorMessage *string
}
