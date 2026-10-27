// Package agent holds the pure parts of the AI agent API (docs/tickets/14-agent-api.md):
// the permission catalogue, key generation and hashing, IP masking and the
// rotation grace bounds. It has no database or HTTP dependencies.
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
)

// KeyPrefix starts every agent key. A key is KeyPrefix followed by the
// unpadded base64url encoding (RFC 4648 §5) of 32 random bytes, 47 characters
// in total.
const KeyPrefix = "jla_"

// DisplayPrefixLength is how many leading characters of a key are stored and
// shown: KeyPrefix plus 8 characters.
const DisplayPrefixLength = 12

// Rotation grace period bounds, in minutes (24 hours at most).
const (
	MinGraceMinutes = 1
	MaxGraceMinutes = 1440
)

// Permission is one "resource.read" or "resource.write" grant.
type Permission string

// The 16 permissions an agent can hold, in catalogue order. There is
// deliberately no agents.* permission: an agent can never manage agents.
const (
	PostsRead     Permission = "posts.read"
	PostsWrite    Permission = "posts.write"
	PagesRead     Permission = "pages.read"
	PagesWrite    Permission = "pages.write"
	MediaRead     Permission = "media.read"
	MediaWrite    Permission = "media.write"
	ProductsRead  Permission = "products.read"
	ProductsWrite Permission = "products.write"
	OrdersRead    Permission = "orders.read"
	ThemesRead    Permission = "themes.read"
	ThemesWrite   Permission = "themes.write"
	PluginsRead   Permission = "plugins.read"
	PluginsWrite  Permission = "plugins.write"
	SettingsRead  Permission = "settings.read"
	SettingsWrite Permission = "settings.write"
	LogsRead      Permission = "logs.read"
)

// AllPermissions returns every permission in catalogue order:
// posts.read, posts.write, pages.read, pages.write, media.read, media.write,
// products.read, products.write, orders.read, themes.read, themes.write,
// plugins.read, plugins.write, settings.read, settings.write, logs.read.
func AllPermissions() []Permission {
	return []Permission{
		PostsRead,
		PostsWrite,
		PagesRead,
		PagesWrite,
		MediaRead,
		MediaWrite,
		ProductsRead,
		ProductsWrite,
		OrdersRead,
		ThemesRead,
		ThemesWrite,
		PluginsRead,
		PluginsWrite,
		SettingsRead,
		SettingsWrite,
		LogsRead,
	}
}

// ValidPermission reports whether name is one of AllPermissions.
func ValidPermission(name string) bool {
	for _, permission := range AllPermissions() {
		if name == string(permission) {
			return true
		}
	}
	return false
}

// Resource returns the part before the dot: "posts.write" → "posts". It is
// also the module name written to agent_events.
func (p Permission) Resource() string {
	for i, char := range p {
		if char == '.' {
			return string(p[:i])
		}
	}
	return string(p)
}

// GenerateKey returns a new plaintext key from crypto/rand.
func GenerateKey() (string, error) {
	var randomBytes [32]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", err
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(randomBytes[:]), nil
}

// HashKey returns the lowercase hex SHA-256 of the whole plaintext key,
// including KeyPrefix. Only this hash is stored.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// PrefixOf returns the first DisplayPrefixLength characters of a key, or ""
// when the key is shorter.
func PrefixOf(plaintext string) string {
	if len(plaintext) < DisplayPrefixLength {
		return ""
	}
	return plaintext[:DisplayPrefixLength]
}

// MaskIP keeps the network part of an address: IPv4 drops the last octet
// ("198.51.100.77" → "198.51.100.0"), IPv6 keeps the first 48 bits
// ("2001:db8:abcd:12::5" → "2001:db8:abcd::"). Unparsable input returns "".
func MaskIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if ipv4 := parsed.To4(); ipv4 != nil {
		return ipv4.Mask(net.CIDRMask(24, 32)).String()
	}
	return parsed.Mask(net.CIDRMask(48, 128)).String()
}

// ValidGraceMinutes reports whether n is within MinGraceMinutes..MaxGraceMinutes.
func ValidGraceMinutes(n int) bool {
	return n >= MinGraceMinutes && n <= MaxGraceMinutes
}
