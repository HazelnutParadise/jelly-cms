package core

import (
	"time"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
)

// Integration is an AI integration identity with a bounded set of permissions.
// Its table is created by migration 0025_agents.sql, not AutoMigrate.
type Integration struct {
	ID          uint        `json:"id" gorm:"primaryKey"`
	Name        string      `json:"name" gorm:"size:80;not null"`
	Description string      `json:"description" gorm:"type:text;not null;default:''"`
	Permissions StringArray `json:"permissions" gorm:"type:text[];not null;default:'{}'"`
	OwnerID     *uint       `json:"owner_id" gorm:"index"`
	CreatedBy   *uint       `json:"created_by" gorm:"index"`
	Status      string      `json:"status" gorm:"size:16;not null;default:'active'"`
	// RoleCap, when set, caps the role the integration runs with at admin, editor or
	// author, whichever is lower than the owner's role. Nil means no cap.
	RoleCap    *string        `json:"role_cap" gorm:"column:role_cap;size:16"`
	DisabledAt *time.Time     `json:"disabled_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Integration) TableName() string { return "integrations" }

// IntegrationKey stores only the SHA-256 hash of an integration's one-time displayed key.
type IntegrationKey struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	IntegrationID    uint       `json:"agent_id" gorm:"column:integration_id;not null;index"`
	KeyHash          string     `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	Prefix           string     `json:"prefix" gorm:"size:12;not null"`
	CreatedAt        time.Time  `json:"created_at"`
	CreatedBy        *uint      `json:"created_by" gorm:"index"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	LastUsedIPMasked string     `json:"last_used_ip_masked" gorm:"size:64;not null;default:''"`
	ExpiresAt        *time.Time `json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at"`
	RevokedBy        *uint      `json:"revoked_by" gorm:"index"`
}

func (IntegrationKey) TableName() string { return "integration_keys" }

// IntegrationEvent records an audit entry. Request entries are reserved with a
// pending status and finalized after the response; IntegrationName is a snapshot so
// the entry remains meaningful after the integration is renamed.
type IntegrationEvent struct {
	ID              uint            `json:"id" gorm:"primaryKey"`
	At              time.Time       `json:"at"`
	ActorType       string          `json:"actor_type" gorm:"size:16;not null"`
	ActorUserID     *uint           `json:"actor_user_id" gorm:"index"`
	IntegrationID   uint            `json:"agent_id" gorm:"column:integration_id;not null;index"`
	IntegrationName string          `json:"agent_name" gorm:"column:integration_name;size:80;not null"`
	KeyID           *uint           `json:"key_id"`
	KeyPrefix       string          `json:"key_prefix" gorm:"size:12;not null;default:''"`
	Action          string          `json:"action" gorm:"size:32;not null"`
	Module          string          `json:"module" gorm:"size:16;not null"`
	Method          string          `json:"method" gorm:"size:8;not null;default:''"`
	Route           string          `json:"route" gorm:"size:255;not null;default:''"`
	Status          int             `json:"status" gorm:"not null;default:0"`
	IPMasked        string          `json:"ip_masked" gorm:"size:64;not null;default:''"`
	Summary         json.RawMessage `json:"summary" gorm:"type:jsonb;not null;default:'{}'"`
	RequestID       string          `json:"request_id" gorm:"size:64;not null;default:''"`
}

func (IntegrationEvent) TableName() string { return "integration_events" }
