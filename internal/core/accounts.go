package core

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

// The structs below mirror the tables created by migrations 0003 to 0007.
// The SQL owns the schema: none of them is passed to AutoMigrate, and the
// gorm tags only describe the existing columns.

// LoginSession is one signed-in device. ID is hashLoginID(jti), the same value
// as RevokedLogin.ID; the raw login ID is never stored.
type LoginSession struct {
	ID          string     `json:"id" gorm:"primaryKey;size:64"`
	UserID      uint       `json:"user_id" gorm:"not null"`
	Method      string     `json:"method" gorm:"size:32;not null;default:'password'"`
	DeviceLabel string     `json:"device_label" gorm:"size:120;not null;default:''"`
	UAHash      string     `json:"-" gorm:"column:ua_hash;size:64;not null;default:''"`
	IPMasked    string     `json:"ip_masked" gorm:"column:ip_masked;size:64;not null;default:''"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// TableName binds LoginSession to login_sessions.
func (LoginSession) TableName() string { return "login_sessions" }

// APIToken is a personal access token. Only TokenHash is stored; Last4 is kept
// for display. Scopes are role templates and never exceed the owner's role.
type APIToken struct {
	ID               uint        `json:"id" gorm:"primaryKey"`
	UserID           uint        `json:"user_id" gorm:"not null"`
	Name             string      `json:"name" gorm:"size:80;not null"`
	Note             string      `json:"note" gorm:"type:text;not null;default:''"`
	TokenHash        string      `json:"-" gorm:"column:token_hash;size:64;not null"`
	Last4            string      `json:"last4" gorm:"column:last4;size:4;not null"`
	Scopes           StringArray `json:"scopes" gorm:"type:text[];not null;default:'{}'"`
	ExpiresAt        *time.Time  `json:"expires_at"`
	RevokedAt        *time.Time  `json:"revoked_at"`
	LastUsedAt       *time.Time  `json:"last_used_at"`
	LastUsedIPMasked string      `json:"last_used_ip_masked" gorm:"column:last_used_ip_masked;size:64;not null;default:''"`
	CreatedAt        time.Time   `json:"created_at"`
}

// TableName binds APIToken to api_tokens.
func (APIToken) TableName() string { return "api_tokens" }

// Notification is one row in a user's notification center. Unread rows that
// share GroupKey are merged by bumping Count.
type Notification struct {
	ID           uint            `json:"id" gorm:"primaryKey"`
	UserID       uint            `json:"user_id" gorm:"not null"`
	Kind         string          `json:"kind" gorm:"size:64;not null"`
	Category     string          `json:"category" gorm:"size:16;not null"`
	TitleKey     string          `json:"title_key" gorm:"column:title_key;size:120;not null;default:''"`
	Params       json.RawMessage `json:"params" gorm:"type:jsonb;not null;default:'{}'"`
	Actions      json.RawMessage `json:"actions" gorm:"type:jsonb;not null;default:'[]'"`
	GroupKey     string          `json:"group_key" gorm:"size:255;not null;default:''"`
	Count        int             `json:"count" gorm:"not null;default:1"`
	InAppVisible bool            `json:"in_app_visible" gorm:"column:in_app_visible;not null;default:true"`
	ReadAt       *time.Time      `json:"read_at"`
	CreatedAt    time.Time       `json:"created_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

// TableName binds Notification to notifications.
func (Notification) TableName() string { return "notifications" }

// NotificationSettings holds per-user webhook and quiet-hours settings.
// Empty strings mean "not set".
type NotificationSettings struct {
	UserID     uint   `json:"user_id" gorm:"primaryKey"`
	WebhookURL string `json:"webhook_url" gorm:"type:text;not null;default:''"`
	QuietStart string `json:"quiet_start" gorm:"size:5;not null;default:''"`
	QuietEnd   string `json:"quiet_end" gorm:"size:5;not null;default:''"`
	QuietTZ    string `json:"quiet_tz" gorm:"column:quiet_tz;size:64;not null;default:''"`
}

// TableName binds NotificationSettings to notification_settings.
func (NotificationSettings) TableName() string { return "notification_settings" }

// NotificationPref overrides the default channels for one event. A missing row
// means the default: in-app on, email and webhook off. Bool fields carry no
// gorm default, so false values are always written.
type NotificationPref struct {
	UserID  uint   `json:"user_id" gorm:"primaryKey"`
	Event   string `json:"event" gorm:"primaryKey;size:64"`
	InApp   bool   `json:"in_app" gorm:"column:in_app;not null"`
	Email   bool   `json:"email" gorm:"not null"`
	Webhook bool   `json:"webhook" gorm:"not null"`
}

// TableName binds NotificationPref to notification_prefs.
func (NotificationPref) TableName() string { return "notification_prefs" }

// NotificationDelivery is one queued email or webhook send. Status held means
// the send waits for quiet hours to end or for a sender to be configured.
type NotificationDelivery struct {
	ID             uint            `json:"id" gorm:"primaryKey"`
	NotificationID uint            `json:"notification_id" gorm:"column:notification_id"`
	Kind           string          `json:"kind" gorm:"size:64;not null;default:''"`
	ToAddress      string          `json:"to_address" gorm:"column:to_address;type:text;not null;default:''"`
	Channel        string          `json:"channel" gorm:"size:16;not null"`
	Status         string          `json:"status" gorm:"size:16;not null;default:'pending'"`
	Attempts       int             `json:"attempts" gorm:"not null;default:0"`
	NextAttemptAt  time.Time       `json:"next_attempt_at"`
	Error          string          `json:"error" gorm:"type:text;not null;default:''"`
	Params         json.RawMessage `json:"params" gorm:"type:jsonb;not null;default:'{}'"`
}

// TableName binds NotificationDelivery to notification_deliveries.
func (NotificationDelivery) TableName() string { return "notification_deliveries" }

// UserTOTP holds a user's two-step verification secret. SecretEnc is AES-GCM
// ciphertext under the totp_key from auth-secrets.json. EnabledAt is nil while
// the secret is enrolled but not yet verified.
type UserTOTP struct {
	UserID      uint       `json:"user_id" gorm:"primaryKey"`
	SecretEnc   []byte     `json:"-" gorm:"column:secret_enc;type:bytea;not null"`
	EnabledAt   *time.Time `json:"enabled_at"`
	LastStep    int64      `json:"-" gorm:"column:last_step;not null;default:0"`
	FailedCount int        `json:"-" gorm:"column:failed_count;not null;default:0"`
	LockedUntil *time.Time `json:"-"`
}

// TableName binds UserTOTP to user_totp.
func (UserTOTP) TableName() string { return "user_totp" }

// RecoveryCode is one of the ten single-use backup codes. Only the hash is stored.
type RecoveryCode struct {
	ID       uint       `json:"id" gorm:"primaryKey"`
	UserID   uint       `json:"user_id" gorm:"not null"`
	CodeHash string     `json:"-" gorm:"column:code_hash;size:64;not null"`
	UsedAt   *time.Time `json:"used_at"`
}

// TableName binds RecoveryCode to user_recovery_codes.
func (RecoveryCode) TableName() string { return "user_recovery_codes" }

// UserInvite is a single-use link: an invitation, a password reset or an
// email confirmation, by Kind. Only TokenHash is stored.
type UserInvite struct {
	ID         uint       `json:"id" gorm:"primaryKey"`
	UserID     uint       `json:"user_id" gorm:"not null"`
	Kind       string     `json:"kind" gorm:"size:16;not null"`
	TokenHash  string     `json:"-" gorm:"column:token_hash;size:64;not null"`
	Role       string     `json:"role" gorm:"size:32;not null;default:''"`
	Note       string     `json:"note" gorm:"type:text;not null;default:''"`
	InvitedBy  *uint      `json:"invited_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName binds UserInvite to user_invites.
func (UserInvite) TableName() string { return "user_invites" }

// StringArray maps a PostgreSQL text[] column to a Go slice.
type StringArray []string

// Value encodes the slice as a PostgreSQL array literal.
func (a StringArray) Value() (driver.Value, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, item := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for j := 0; j < len(item); j++ {
			if item[j] == '"' || item[j] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(item[j])
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// Scan decodes a PostgreSQL text[] literal.
func (a *StringArray) Scan(src any) error {
	var text string
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("core: cannot scan %T into StringArray", src)
	}
	items, err := parsePGTextArray(text)
	if err != nil {
		return err
	}
	*a = items
	return nil
}

// parsePGTextArray reads a one-dimensional PostgreSQL text array literal such
// as {a,"b c","d\"e"}.
func parsePGTextArray(text string) ([]string, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '{' || text[len(text)-1] != '}' {
		return nil, errors.New("core: malformed text array")
	}
	body := text[1 : len(text)-1]
	items := []string{}
	for i := 0; i < len(body); {
		var item strings.Builder
		if body[i] == '"' {
			i++
			closed := false
			for i < len(body) && !closed {
				switch c := body[i]; {
				case c == '\\' && i+1 < len(body):
					item.WriteByte(body[i+1])
					i += 2
				case c == '"':
					closed = true
					i++
				default:
					item.WriteByte(c)
					i++
				}
			}
			if !closed {
				return nil, errors.New("core: malformed text array")
			}
		} else {
			start := i
			for i < len(body) && body[i] != ',' {
				i++
			}
			item.WriteString(strings.TrimSpace(body[start:i]))
		}
		items = append(items, item.String())
		if i < len(body) {
			if body[i] != ',' {
				return nil, errors.New("core: malformed text array")
			}
			i++
		}
	}
	return items, nil
}
