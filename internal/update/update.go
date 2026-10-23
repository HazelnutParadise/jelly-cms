// Package update checks for new releases and, for standalone binaries,
// downloads, verifies and swaps the executable. Release assets come from this
// repository's GitHub Releases.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AllowedHosts are the only hosts a download may use. Redirects to other hosts are refused.
var AllowedHosts = []string{"api.github.com", "github.com", "objects.githubusercontent.com"}

// MaxDownloadBytes bounds one release archive.
const MaxDownloadBytes = 200 << 20

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("update: not implemented")

// ErrHostNotAllowed is returned when a URL names a host outside AllowedHosts.
var ErrHostNotAllowed = errors.New("update: host is not on the allow list")

// Release describes one published version.
type Release struct {
	Version     string
	Channel     string
	PublishedAt time.Time
	Notes       string
	Assets      []Asset
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name      string
	URL       string
	SHA256    string
	Size      int64
	Signature string // minisign signature URL or inline
}

// State is persisted in data/update-state.json so a restart can tell whether
// the last swap succeeded and roll back after two failed boots.
type State struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest"`
	Previous    string    `json:"previous,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Status      string    `json:"status,omitempty"`
	FailedBoots int       `json:"failed_boots,omitempty"`
}

// ValidateURL accepts only https URLs on AllowedHosts without userinfo.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("update: parse url: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("update: only https urls are allowed")
	}
	if u.User != nil {
		return errors.New("update: userinfo is not allowed")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range AllowedHosts {
		if host == allowed {
			return nil
		}
	}
	return ErrHostNotAllowed
}

// LoadState reads the state file. A missing file yields the zero State.
func LoadState(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("update: decode state: %w", err)
	}
	return st, nil
}

// SaveState writes the state file atomically (temporary file, then rename).
func SaveState(path string, st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".update-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Check asks the release source for the newest version on the configured channel.
// TODO(08c): implement against the GitHub releases API through safehttp.
func Check(ctx context.Context) (Release, error) {
	return Release{}, ErrNotImplemented
}

// Apply downloads, verifies (sha256 and minisign), backs up, swaps and
// restarts into version. Any failed step restores the previous binary.
// TODO(08c): implement.
func Apply(ctx context.Context, version string) error {
	return ErrNotImplemented
}

// Rollback restores jelly-cms.prev as the running binary.
// TODO(08c): implement.
func Rollback(ctx context.Context) error {
	return ErrNotImplemented
}
