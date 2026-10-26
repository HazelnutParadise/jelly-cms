package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// PermissionNamesHash returns the digest a store install must accept for a
// plugin that declares names. It covers the sorted, de-duplicated names, so the
// order in a catalog or a manifest does not matter. The digest is stored in
// plugins.permissions_granted_hash.
func PermissionNamesHash(names []string) string {
	seen := make(map[string]bool, len(names))
	list := make([]string, 0, len(names))
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			list = append(list, name)
		}
	}
	sort.Strings(list)
	sum := sha256.Sum256([]byte(strings.Join(list, "\n")))

	return hex.EncodeToString(sum[:])
}

// ManifestPermissionNames returns the permission names a manifest declares.
func ManifestPermissionNames(meta PluginMetadata) []string {
	names := make([]string, 0, len(meta.Permissions))
	for _, permission := range meta.Permissions {
		names = append(names, permission.Name)
	}

	return names
}
