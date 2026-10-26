package core

import (
	"time"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
)

// Agent is an AI agent identity with a bounded set of permissions.
// Its table is created by migration 0025_agents.sql, not AutoMigrate.
type Agent struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	Name        string         `json:"name" gorm:"size:80;not null"`
	Description string         `json:"description" gorm:"type:text;not null;default:''"`
	Permissions StringArray    `json:"permissions" gorm:"type:text[];not null;default:'{}'"`
	OwnerID     *uint          `json:"owner_id" gorm:"index"`
	CreatedBy   *uint          `json:"created_by" gorm:"index"`
	Status      string         `json:"status" gorm:"size:16;not null;default:'active'"`
	DisabledAt  *time.Time     `json:"disabled_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Agent) TableName() string { return "agents" }

// AgentKey stores only the SHA-256 hash of an agent's one-time displayed key.
type AgentKey struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	AgentID          uint       `json:"agent_id" gorm:"not null;index"`
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

func (AgentKey) TableName() string { return "agent_keys" }

// AgentEvent records an audit entry. Request entries are reserved with a
// pending status and finalized after the response; AgentName is a snapshot so
// the entry remains meaningful after the agent is renamed.
type AgentEvent struct {
	ID          uint            `json:"id" gorm:"primaryKey"`
	At          time.Time       `json:"at"`
	ActorType   string          `json:"actor_type" gorm:"size:8;not null"`
	ActorUserID *uint           `json:"actor_user_id" gorm:"index"`
	AgentID     uint            `json:"agent_id" gorm:"not null;index"`
	AgentName   string          `json:"agent_name" gorm:"size:80;not null"`
	KeyID       *uint           `json:"key_id"`
	KeyPrefix   string          `json:"key_prefix" gorm:"size:12;not null;default:''"`
	Action      string          `json:"action" gorm:"size:32;not null"`
	Module      string          `json:"module" gorm:"size:16;not null"`
	Method      string          `json:"method" gorm:"size:8;not null;default:''"`
	Route       string          `json:"route" gorm:"size:255;not null;default:''"`
	Status      int             `json:"status" gorm:"not null;default:0"`
	IPMasked    string          `json:"ip_masked" gorm:"size:64;not null;default:''"`
	Summary     json.RawMessage `json:"summary" gorm:"type:jsonb;not null;default:'{}'"`
	RequestID   string          `json:"request_id" gorm:"size:64;not null;default:''"`
}

func (AgentEvent) TableName() string { return "agent_events" }
