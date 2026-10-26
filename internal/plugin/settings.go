package plugin

import (
	"fmt"
	"unicode/utf8"
)

// FindSetting returns the declaration of key.
func FindSetting(specs []SettingSpec, key string) (SettingSpec, bool) {
	for _, spec := range specs {
		if spec.Key == key {
			return spec, true
		}
	}

	return SettingSpec{}, false
}

// CheckSettingValues validates a write. Every key must be declared, and every
// non-nil value must match the declared type and range. A nil value means the
// setting is cleared. Error messages name the setting but never its value, so
// a rejected secret does not leak into a response or a log line.
func CheckSettingValues(specs []SettingSpec, values map[string]any) error {
	for key, value := range values {
		spec, ok := FindSetting(specs, key)
		if !ok {
			return fmt.Errorf("setting %q is not declared by the plugin", key)
		}

		if err := checkSettingValue(spec, value); err != nil {
			return err
		}
	}

	return nil
}

// MaskSecret hides a secret for display. It keeps the last four characters
// and replaces the rest with "****". A value of four characters or fewer is
// fully hidden.
func MaskSecret(value string) string {
	runes := []rune(value)
	if utf8.RuneCountInString(value) <= 4 {
		return "****"
	}

	return "****" + string(runes[len(runes)-4:])
}

// IsSecretSetting reports whether key holds a secret.
func IsSecretSetting(specs []SettingSpec, key string) bool {
	spec, ok := FindSetting(specs, key)

	return ok && spec.Type == SettingSecret
}
