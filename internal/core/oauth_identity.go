package core

import (
	"time"

	"gorm.io/gorm"
)

// OAuthIdentity binds one stable provider subject to a Jelly user.
type OAuthIdentity struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	UserID    uint           `json:"user_id" gorm:"not null"`
	Provider  string         `json:"provider" gorm:"not null"`
	Subject   string         `json:"-" gorm:"not null"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (OAuthIdentity) TableName() string { return "oauth_identities" }
