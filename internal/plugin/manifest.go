package plugin

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
)

// Setting value types a manifest may declare.
const (
	SettingString = "string"
	SettingSecret = "secret"
	SettingInt    = "int"
	SettingBool   = "bool"
)

// Limits on manifest v2 lists. They keep a hostile manifest from making the
// admin screens or the validator do unbounded work.
const (
	maxSettings    = 64
	maxPermissions = 64
	maxActions     = 32
	maxExtensions  = 16
	maxSettingText = 64 << 10
)

// enforcedPermissions are the permissions the runtime checks. Every other
// declared permission is shown as declared but not enforced.
var enforcedPermissions = map[string]bool{
	"http.fetch": true,
	"store":      true,
	"settings":   true,
}

var (
	settingKeyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	actionIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	permissionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

// SettingSpec declares one setting a plugin accepts. Min and Max apply to int
// settings only.
type SettingSpec struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Label   string   `json:"label,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Default any      `json:"default,omitempty"`
}

// PermissionSpec declares one permission the plugin asks for.
type PermissionSpec struct {
	Name string `json:"name"`
}

// Enforced reports whether the runtime enforces the permission.
func (p PermissionSpec) Enforced() bool {
	return enforcedPermissions[p.Name]
}

// ActionSpec declares an action an admin can run from the plugin screen.
type ActionSpec struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// ExtensionSpec declares that the plugin offers an implementation of an
// extension point.
type ExtensionSpec struct {
	Point string `json:"point"`
}

// ParseManifest decodes plugin.json and validates the manifest v2 lists. It
// does not require an entry script, so a plugin that is not running can still
// be shown. Runtime loading applies validatePluginMeta on top of it.
func ParseManifest(data []byte) (PluginMetadata, error) {
	var meta PluginMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("invalid JSON: %w", err)
	}

	if !pluginIDPattern.MatchString(meta.ID) {
		return meta, fmt.Errorf("plugin id %q is missing or invalid", meta.ID)
	}

	if err := validateSettingSpecs(meta.Settings); err != nil {
		return meta, err
	}
	if err := validatePermissionSpecs(meta.Permissions); err != nil {
		return meta, err
	}
	if err := validateActionSpecs(meta.Actions); err != nil {
		return meta, err
	}
	if err := validateExtensionSpecs(meta.Extensions); err != nil {
		return meta, err
	}

	return meta, nil
}

// ReadManifest reads and validates plugin.json inside dir.
func ReadManifest(dir string) (PluginMetadata, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return PluginMetadata{}, fmt.Errorf("plugin directory %s: %w", dir, err)
	}

	if !info.IsDir() {
		return PluginMetadata{}, fmt.Errorf("plugin path %s is not a directory", dir)
	}

	manifestPath, err := resolvePluginFile(dir, "plugin.json")
	if err != nil {
		return PluginMetadata{}, fmt.Errorf("plugin manifest: %w", err)
	}

	data, err := readFileLimited(manifestPath, maxManifestBytes)
	if err != nil {
		return PluginMetadata{}, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	meta, err := ParseManifest(data)
	if err != nil {
		return PluginMetadata{}, fmt.Errorf("invalid manifest %s: %w", manifestPath, err)
	}

	return meta, nil
}

func validateSettingSpecs(specs []SettingSpec) error {
	if len(specs) > maxSettings {
		return fmt.Errorf("a manifest declares at most %d settings", maxSettings)
	}

	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if !settingKeyPattern.MatchString(spec.Key) {
			return fmt.Errorf("setting key %q is invalid", spec.Key)
		}

		if seen[spec.Key] {
			return fmt.Errorf("setting %q is declared twice", spec.Key)
		}
		seen[spec.Key] = true

		switch spec.Type {
		case SettingString, SettingSecret, SettingBool:
			if spec.Min != nil || spec.Max != nil {
				return fmt.Errorf("setting %q: min and max apply to int settings only", spec.Key)
			}
		case SettingInt:
			if spec.Min != nil && spec.Max != nil && *spec.Min > *spec.Max {
				return fmt.Errorf("setting %q: min is greater than max", spec.Key)
			}
		default:
			return fmt.Errorf("setting %q has unknown type %q", spec.Key, spec.Type)
		}

		if spec.Default != nil {
			if spec.Type == SettingSecret {
				return fmt.Errorf("setting %q: a secret cannot declare a default", spec.Key)
			}
			if err := checkSettingValue(spec, spec.Default); err != nil {
				return fmt.Errorf("setting %q default: %w", spec.Key, err)
			}
		}
	}

	return nil
}

func validatePermissionSpecs(permissions []PermissionSpec) error {
	if len(permissions) > maxPermissions {
		return fmt.Errorf("a manifest declares at most %d permissions", maxPermissions)
	}

	seen := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		if !permissionNamePattern.MatchString(permission.Name) {
			return fmt.Errorf("permission name %q is invalid", permission.Name)
		}

		if seen[permission.Name] {
			return fmt.Errorf("permission %q is declared twice", permission.Name)
		}
		seen[permission.Name] = true
	}

	return nil
}

func validateActionSpecs(actions []ActionSpec) error {
	if len(actions) > maxActions {
		return fmt.Errorf("a manifest declares at most %d actions", maxActions)
	}

	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		if !actionIDPattern.MatchString(action.ID) {
			return fmt.Errorf("action id %q is invalid", action.ID)
		}

		if seen[action.ID] {
			return fmt.Errorf("action %q is declared twice", action.ID)
		}
		seen[action.ID] = true
	}

	return nil
}

func validateExtensionSpecs(extensions []ExtensionSpec) error {
	if len(extensions) > maxExtensions {
		return fmt.Errorf("a manifest declares at most %d extensions", maxExtensions)
	}

	for _, extension := range extensions {
		if !ext.Known(extension.Point) {
			return fmt.Errorf("extension point %q is unknown", extension.Point)
		}
	}

	return nil
}

// checkSettingValue validates one value against its declaration. A nil value
// is accepted here; callers decide what nil means for them.
func checkSettingValue(spec SettingSpec, value any) error {
	if value == nil {
		return nil
	}

	switch spec.Type {
	case SettingString, SettingSecret:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("setting %q must be a string", spec.Key)
		}
		if len(text) > maxSettingText {
			return fmt.Errorf("setting %q is longer than %d bytes", spec.Key, maxSettingText)
		}
	case SettingBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("setting %q must be true or false", spec.Key)
		}
	case SettingInt:
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || math.Abs(number) > 1<<53 {
			return fmt.Errorf("setting %q must be a whole number", spec.Key)
		}
		if spec.Min != nil && number < *spec.Min {
			return fmt.Errorf("setting %q must be at least %v", spec.Key, *spec.Min)
		}
		if spec.Max != nil && number > *spec.Max {
			return fmt.Errorf("setting %q must be at most %v", spec.Key, *spec.Max)
		}
	default:
		return fmt.Errorf("setting %q has unknown type %q", spec.Key, spec.Type)
	}

	return nil
}
