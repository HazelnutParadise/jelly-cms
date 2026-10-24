// Package redirects holds URL redirect rules and the 404 log for the
// redirects page. Rules are stored in the redirects table (migration 0017).
package redirects

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Status codes a rule may use. 410 means gone and has no target.
const (
	Moved = 301
	Found = 302
	Gone  = 410
)

// MaxChainDepth is the redirect chain depth checked when a rule is saved.
const MaxChainDepth = 5

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("redirects: not implemented")

// ErrInvalidTarget is returned by Validate for targets that are not allowed.
var ErrInvalidTarget = errors.New("redirects: invalid target")

// Redirect is one rule.
type Redirect struct {
	ID           uint
	FromPath     string
	ToURL        string
	Kind         int // Moved, Found or Gone
	IsRule       bool
	KeepQuery    bool
	Source       string // manual|auto|import
	ExpiresAt    *time.Time
	Hits         int64
	LastHitAt    *time.Time
	TargetStatus int
	CheckedAt    *time.Time
}

// Match returns the rule for path. The skeleton has no rules loaded yet.
// TODO(08b): load rules from the redirects table and cache them.
func Match(path string) (*Redirect, bool) {
	return nil, false
}

// Validate checks a redirect target. Accepted forms are a same-site path that
// starts with a single "/", or an absolute http(s) URL without userinfo.
// Everything else, including javascript:, data:, CRLF and a leading "$", is refused.
func Validate(to string) error {
	if to == "" {
		return ErrInvalidTarget
	}
	for _, r := range to {
		if r < 0x20 || r == 0x7f {
			return ErrInvalidTarget
		}
	}
	if strings.HasPrefix(to, "$") {
		return ErrInvalidTarget
	}
	if strings.HasPrefix(to, "/") {
		if strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\") {
			return ErrInvalidTarget
		}
		return nil
	}
	u, err := url.Parse(to)
	if err != nil {
		return ErrInvalidTarget
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrInvalidTarget
	}
	if u.Host == "" || u.User != nil {
		return ErrInvalidTarget
	}
	return nil
}

var (
	hitMu   sync.Mutex
	hitRows = map[string]int64{}
)

// CountHit adds one hit for rule path in memory. FlushHits writes the totals
// back every 30 seconds.
func CountHit(path string) {
	hitMu.Lock()
	defer hitMu.Unlock()
	hitRows[path]++
}

// FlushHits writes the accumulated hit counts to the database.
// TODO(08b): run as the redirects.flush_hits job.
func FlushHits() error {
	return ErrNotImplemented
}
