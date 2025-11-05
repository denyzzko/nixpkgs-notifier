package database

import "time"

type PackageRow struct {
	ID        int64
	CreatedAt time.Time
	Name      string
	Version   string
}

type TrackingRow struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	UserID       int64
	PackageID    int64
	UsersVersion string
}

type AccountRow struct {
	UserID        int64
	CreatedAt     time.Time
	Provider      string
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}
