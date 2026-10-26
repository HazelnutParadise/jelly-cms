// Package store holds the plugin store catalog format, the built-in official
// list and the signing key checks. A catalog is a JSON document with a minisign
// signature beside it. The server fetches, verifies and stores it; see
// internal/server/store_api.go.
package store

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// officialJSON is the built-in official list. It is used when no remote
// catalog is configured.
//
//go:embed official.json
var officialJSON []byte

// officialPublicKey is the minisign public key line that signs the catalog and
// the official archives. It stays empty until the maintainer generates the key.
// Nothing signed by it verifies while it is empty, so official items cannot be
// installed.
const officialPublicKey = ""

// KindPlugin is the only item kind the store installs.
const KindPlugin = "plugin"

// MaxItems caps the number of items one catalog may list.
const MaxItems = 500

const (
	envPublicKey    = "JELLY_STORE_PUBLIC_KEY"
	envCatalogURL   = "JELLY_STORE_CATALOG_URL"
	envTestLoopback = "JELLY_TEST_ALLOW_LOOPBACK"
)

// ErrNoPublicKey means no signing key is configured, so nothing can be verified.
var ErrNoPublicKey = errors.New("store: no minisign public key is configured")

var (
	permissionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	sha256Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Item is one entry of a catalog. URL and SHA256 describe the archive to
// download; both are optional in the catalog and checked when present.
type Item struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Category    string   `json:"category"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Official    bool     `json:"official"`
	Permissions []string `json:"permissions"`
	URL         string   `json:"url,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
}

type document struct {
	Items []Item `json:"items"`
}

// Embedded returns the built-in official list.
func Embedded() ([]Item, error) {
	return ParseCatalog(officialJSON)
}

// ParseCatalog decodes and validates catalog JSON. It does not check the
// signature; callers verify the bytes first.
func ParseCatalog(raw []byte) ([]Item, error) {
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("store: catalog is not valid JSON: %w", err)
	}
	if len(doc.Items) > MaxItems {
		return nil, fmt.Errorf("store: a catalog lists at most %d items", MaxItems)
	}

	seen := make(map[string]bool, len(doc.Items))
	for i, item := range doc.Items {
		if err := validateItem(item); err != nil {
			return nil, fmt.Errorf("store: item %d: %w", i, err)
		}
		if seen[item.ID] {
			return nil, fmt.Errorf("store: item %q is listed twice", item.ID)
		}
		seen[item.ID] = true
	}

	return doc.Items, nil
}

func validateItem(item Item) error {
	switch {
	case !plugin.ValidPluginID(item.ID):
		return fmt.Errorf("id %q is missing or invalid", item.ID)
	case item.Kind != KindPlugin:
		return fmt.Errorf("kind %q is not supported", item.Kind)
	case strings.TrimSpace(item.Name) == "" || len(item.Name) > 255:
		return fmt.Errorf("%s: name must be 1 to 255 bytes", item.ID)
	case item.Version == "" || len(item.Version) > 64:
		return fmt.Errorf("%s: version must be 1 to 64 bytes", item.ID)
	case len(item.Category) > 64:
		return fmt.Errorf("%s: category is longer than 64 bytes", item.ID)
	case len(item.Description) > 2000:
		return fmt.Errorf("%s: description is longer than 2000 bytes", item.ID)
	case len(item.Permissions) > 64:
		return fmt.Errorf("%s: at most 64 permissions", item.ID)
	}

	for _, name := range item.Permissions {
		if !permissionNamePattern.MatchString(name) {
			return fmt.Errorf("%s: permission %q is invalid", item.ID, name)
		}
	}

	if item.URL != "" {
		u, err := url.Parse(item.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("%s: url must be an absolute https address", item.ID)
		}
	}
	if item.SHA256 != "" && !sha256Pattern.MatchString(item.SHA256) {
		return fmt.Errorf("%s: sha256 must be 64 lowercase hex characters", item.ID)
	}

	return nil
}

// TestSeamsActive reports whether the test-only seams are honoured. They are
// honoured only when JELLY_TEST_ALLOW_LOOPBACK is exactly "1".
func TestSeamsActive() bool { return os.Getenv(envTestLoopback) == "1" }

// PublicKeyLine returns the minisign public key line for the catalog and the
// official archives. JELLY_STORE_PUBLIC_KEY replaces the built-in key only in a
// test run. An empty result means no key is configured.
func PublicKeyLine() string {
	if TestSeamsActive() {
		if v := strings.TrimSpace(os.Getenv(envPublicKey)); v != "" {
			return v
		}
	}

	return officialPublicKey
}

// CatalogURL returns the remote catalog location from JELLY_STORE_CATALOG_URL,
// or "" to use the built-in list.
func CatalogURL() string {
	return strings.TrimSpace(os.Getenv(envCatalogURL))
}
