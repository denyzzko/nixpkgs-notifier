package database

import "time"

type User struct {
	ID        int64
	UserName  string
	Email     string
	Role      string
	CreatedAt time.Time
}

/*
type Package struct {
	ID   int64
	Name string
}
*/

type UserPackage struct {
	UserID           int64
	PackageID        int64
	LastKnownVersion *string
}
