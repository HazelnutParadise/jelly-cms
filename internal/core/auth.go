package core

import "time"

// RevokedLogin stores the SHA-256 hash of a login ID that has been revoked.
type RevokedLogin struct {
	ID        string    `gorm:"primaryKey;size:64"`
	ExpiresAt time.Time `gorm:"index"`
}
