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
	"sync"
	"sync/atomic"
	"time"
)

// Release source. The API base can be replaced only by the test seams below.
const (
	repoOwner      = "HazelnutParadise"
	repoName       = "jelly-cms"
	defaultAPIBase = "https://api.github.com"
)

// StatePath is the update state file, relative to the working directory like
// the other data files.
const StatePath = "data/update-state.json"

// Channels a site can follow.
const (
	ChannelStable  = "stable"
	ChannelPreview = "preview"
)

// Status values stored in the state file. applying is written before the
// executable is touched. applied means the new executable is in place and waits
// for its first healthy start (see ConfirmStarted).
const (
	StatusApplying   = "applying"
	StatusApplied    = "applied"
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusRolledBack = "rolled_back"
)

// AllowedHosts are the only hosts a download may use. Redirects to other hosts are refused.
var AllowedHosts = []string{"api.github.com", "github.com", "objects.githubusercontent.com"}

// MaxDownloadBytes bounds one release archive, compressed and extracted.
const MaxDownloadBytes = 200 << 20

// Sentinel errors. Handlers map them to status codes.
var (
	// ErrHostNotAllowed is returned when a URL names a host outside AllowedHosts.
	ErrHostNotAllowed = errors.New("update: host is not on the allow list")
	// ErrNotNewer refuses a target that is not newer than the running version.
	ErrNotNewer = errors.New("update: target version is not newer than the running version")
	// ErrNoRelease means the release source has no matching release.
	ErrNoRelease = errors.New("update: no release matches the requested version")
	// ErrNotWritable means the executable's directory does not accept new files.
	ErrNotWritable = errors.New("update: the executable directory is not writable")
	// ErrVerification covers sha256 and minisign failures.
	ErrVerification = errors.New("update: release verification failed")
	// ErrArchive means the archive holds an entry the updater does not accept.
	ErrArchive = errors.New("update: archive contents are not allowed")
	// ErrHealthcheck means the installed executable did not pass its healthcheck.
	ErrHealthcheck = errors.New("update: healthcheck of the new executable failed")
	// ErrNoPrevious means there is no previous executable to roll back to.
	ErrNoPrevious = errors.New("update: no previous version to roll back to")
	// ErrNoSigningKey means no minisign public key is available to verify releases.
	ErrNoSigningKey = errors.New("update: no minisign public key is configured")
	// ErrBusy means another apply or rollback is running.
	ErrBusy = errors.New("update: another update operation is running")
)

// RuntimeError is returned when this process cannot replace its own executable.
// Kind is the runtime the process runs in (container, kubernetes or package).
type RuntimeError struct{ Kind string }

func (e *RuntimeError) Error() string {
	return "update: runtime " + e.Kind + " cannot replace the executable"
}

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
	LastError   string    `json:"last_error,omitempty"`
}

// Test seams. The harness sets JELLY_TEST_ALLOW_LOOPBACK=1 together with the
// others. Production never sets the loopback flag, so the seams stay inert there.
const (
	envTestLoopback = "JELLY_TEST_ALLOW_LOOPBACK"
	envAPIBase      = "JELLY_UPDATE_API_BASE"
	envPublicKey    = "JELLY_UPDATE_PUBLIC_KEY"
	envExecutable   = "JELLY_UPDATE_EXECUTABLE"
)

// officialPublicKey is injected into release builds. Empty stays fail-closed,
// so an unsigned or unverifiable release is never installed.
var officialPublicKey string

// TestSeamsActive reports whether the test seams are honoured.
func TestSeamsActive() bool { return os.Getenv(envTestLoopback) == "1" }

func apiBase() string {
	if TestSeamsActive() {
		if v := strings.TrimSpace(os.Getenv(envAPIBase)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return defaultAPIBase
}

// publicKeyLine returns the minisign public key line, or "" when none is set.
func publicKeyLine() string {
	if TestSeamsActive() {
		if v := strings.TrimSpace(os.Getenv(envPublicKey)); v != "" {
			return v
		}
	}
	return officialPublicKey
}

// executablePath returns the file that Apply swaps.
func executablePath() (string, error) {
	if TestSeamsActive() {
		if v := os.Getenv(envExecutable); v != "" {
			return v, nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// allowURL accepts a download URL. Outside tests that means ValidateURL. In a
// test run, URLs under the fake API base are accepted as well.
func allowURL(raw string) error {
	if TestSeamsActive() {
		if base := strings.TrimRight(strings.TrimSpace(os.Getenv(envAPIBase)), "/"); base != "" && strings.HasPrefix(raw, base+"/") {
			return nil
		}
	}
	return ValidateURL(raw)
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

// stateMu serialises read-modify-write cycles on the state file.
var stateMu sync.Mutex

// mutateState loads the state file, applies fn and saves it when fn reports a
// change. A file that does not exist is created on the first change.
func mutateState(fn func(*State) bool) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := LoadState(StatePath)
	if err != nil {
		return err
	}
	if !fn(&st) {
		return nil
	}
	return SaveState(StatePath, st)
}

// Operations that change the executable run one at a time. Cancel stops the
// running one at its next checkpoint.
var (
	opMu     sync.Mutex
	cancelMu sync.Mutex
	cancelOp context.CancelFunc
)

func lockOp() (func(), error) {
	if !opMu.TryLock() {
		return nil, ErrBusy
	}
	return opMu.Unlock, nil
}

func beginOp(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	cancelMu.Lock()
	cancelOp = cancel
	cancelMu.Unlock()
	return ctx, func() {
		cancelMu.Lock()
		cancelOp = nil
		cancelMu.Unlock()
		cancel()
	}
}

// Cancel stops the running apply or rollback at its next checkpoint. It reports
// false when no operation is running.
func Cancel() bool {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	if cancelOp == nil {
		return false
	}
	cancelOp()
	return true
}

// Restart requests a graceful shutdown followed by a relaunch of the executable,
// which then runs the new version. The server calls it after a successful apply
// or rollback, once the response is written.
var (
	restartOnce    sync.Once
	restartCh      = make(chan struct{})
	restartPending atomic.Bool
)

// RequestRestart asks the process to shut down and relaunch.
func RequestRestart() {
	restartOnce.Do(func() {
		restartPending.Store(true)
		close(restartCh)
	})
}

// RestartRequested is closed when RequestRestart is called.
func RestartRequested() <-chan struct{} { return restartCh }

// RestartPending reports whether RequestRestart was called.
func RestartPending() bool { return restartPending.Load() }
