// Package settings reads and writes the registered site options (table
// options). Only keys registered here can be written through the admin API;
// every write is audited with its actor and key names, never its values.
//
// Shared with ticket 08 (settings): Register is called from seed.go and by
// feature packages that add their own keys.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Value kinds accepted by Spec.Kind.
const (
	KindString = "string"
	KindInt    = "int"
	KindBool   = "bool"
	KindJSON   = "json"
	KindSecret = "secret"
)

// DefaultMaxBytes bounds one stored value when Spec.MaxBytes is zero.
const DefaultMaxBytes = 64 << 10

// Errors returned by Set. The admin API maps them to 400 and 413.
var (
	ErrUnknownKey      = errors.New("settings: key is not registered")
	ErrTooLarge        = errors.New("settings: value exceeds the size limit")
	ErrInvalidValue    = errors.New("settings: value does not match the key's type")
	ErrNotImplemented  = errors.New("settings: not implemented")
	ErrDatabaseMissing = errors.New("settings: database is not initialized")
)

// Spec describes one registered option.
type Spec struct {
	Key  string
	Kind string // string|int|bool|json|secret
	// Default is returned by Get when the key has no stored row.
	Default  string
	MaxBytes int
	// Tab names the settings tab that edits the key (see server/settings_tabs.go).
	Tab string
}

var (
	regMu    sync.RWMutex
	registry = map[string]Spec{}
)

// Register adds specs. A later registration of the same key replaces the earlier one.
func Register(specs ...Spec) {
	regMu.Lock()
	defer regMu.Unlock()
	for _, s := range specs {
		registry[s.Key] = s
	}
}

// Lookup returns the spec for key and whether it is registered.
func Lookup(key string) (Spec, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := registry[key]
	return s, ok
}

// Specs returns every registered spec sorted by key.
func Specs() []Spec {
	regMu.RLock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	regMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Get returns the plaintext value for key, or the registered default when the
// row is missing, the database is not ready or the key is unknown. Sealed
// values are opened here; a value that cannot be opened returns "" and is logged.
// TODO(08a): cache the reads through internal/cache.
func Get(ctx context.Context, key string) string {
	spec, ok := Lookup(key)
	if !ok {
		return ""
	}
	if data.DB == nil {
		return spec.Default
	}
	var opt core.Option
	if err := data.DB.WithContext(ctx).Where("key = ?", key).First(&opt).Error; err != nil {
		return spec.Default
	}
	plain, err := revealStored(key, opt.Value)
	if err != nil {
		log.Printf("settings: reveal %s: %v", key, err)
		return ""
	}
	return plain
}

// Set validates every key and size first, then seals the values that need it,
// writes all values in one transaction and emits one audit entry that lists
// the keys. Nothing is written when any key is unknown or too large.
func Set(ctx context.Context, actor uint, kv map[string]string) error {
	keys := make([]string, 0, len(kv))
	for key, value := range kv {
		spec, ok := Lookup(key)
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		if spec.Tab == "internal" {
			// Internal keys (installation_id and the like) are written by the
			// system, never through the settings API.
			return fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		limit := spec.MaxBytes
		if limit <= 0 {
			limit = DefaultMaxBytes
		}
		if len(value) > limit {
			return fmt.Errorf("%w: %s", ErrTooLarge, key)
		}
		if err := checkKind(spec, value); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidValue, key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if data.DB == nil {
		return ErrDatabaseMissing
	}
	stored := make(map[string]string, len(keys))
	for _, key := range keys {
		value := kv[key]
		if sealed(key) {
			sealedValue, err := sealValue(key, value)
			if err != nil {
				return fmt.Errorf("seal %s: %w", key, err)
			}
			value = sealedValue
		}
		stored[key] = value
	}
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, key := range keys {
			opt := core.Option{Key: key, Value: stored[key]}
			err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).Create(&opt).Error
			if err != nil {
				return fmt.Errorf("save %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	syslog.Audit(ctx, actor, "settings.update", map[string]any{"keys": strings.Join(keys, ",")})
	return nil
}

// checkKind validates value against the kind of spec. An empty value is
// always accepted and means "not set".
func checkKind(spec Spec, value string) error {
	if value == "" {
		return nil
	}
	switch spec.Kind {
	case KindInt:
		_, err := strconv.Atoi(value)
		return err
	case KindBool:
		if value != "true" && value != "false" {
			return ErrInvalidValue
		}
	case KindJSON:
		if !json.Valid([]byte(value)) {
			return ErrInvalidValue
		}
	}
	return nil
}

var secretKey = regexp.MustCompile(`(secret|password|key|token|iv)(_enc)?$`)

// Masked hides the value of keys that hold secrets: the last four characters
// are kept, everything else becomes "****". Short values are fully hidden.
// Empty values stay empty so the form can tell "not set" from "set".
func Masked(key, value string) string {
	if value == "" || !secretKey.MatchString(key) {
		return value
	}
	return maskTail(value)
}

// maskTail keeps the last four characters of a non-empty value.
func maskTail(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}
