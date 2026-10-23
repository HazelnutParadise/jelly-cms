package core

import "time"

// RevokedLogin stores SHA-256 hashes for revoked login IDs and consumed OAuth
// states, distinguished by hash input.
type RevokedLogin struct {
	ID        string    `gorm:"primaryKey;size:64"`
	ExpiresAt time.Time `gorm:"index"`
}
