package core

import "time"

// UserInvite is a minimal stub of the user_invites table that ticket 06 creates
// (migration 0007 on the accounts branch). It is shared with ticket 06: when
// 06 lands, delete this file and keep 06's definition. Shop checkout uses it to
// send the customer.set_password link. Not added to AutoMigrate.
type UserInvite struct {
	ID         uint       `json:"id" gorm:"primaryKey"`
	UserID     uint       `json:"user_id" gorm:"index;not null"`
	Kind       string     `json:"kind" gorm:"size:16;not null"` // invite, reset, email
	TokenHash  string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	Role       string     `json:"role" gorm:"size:20"`
	Note       string     `json:"note" gorm:"type:text"`
	InvitedBy  *uint      `json:"invited_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// TableName keeps the name that ticket 06 uses.
func (UserInvite) TableName() string { return "user_invites" }
