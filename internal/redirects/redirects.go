// Package redirects holds URL redirect rules, their hit counts and the 404 log.
// Rules live in the redirects table and misses in not_found_log (migration 0017).
package redirects

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

// Status codes a rule may use. 410 means gone and has no target.
const (
	Moved = 301
	Found = 302
	Gone  = 410
)

// MaxChainDepth is the most redirect hops one chain may take. Saving a rule
// that makes a longer chain is refused.
const MaxChainDepth = 5

// NotFoundCap is the most distinct paths kept in not_found_log.
const NotFoundCap = 5000

const (
	maxFromPathRunes = 512
	maxToURLBytes    = 2048
	maxMissBytes     = 512
)

// ErrInvalidTarget is returned by Validate for targets that are not allowed.
var ErrInvalidTarget = errors.New("redirects: invalid target")

// ErrDuplicate means another rule already uses the source path.
var ErrDuplicate = errors.New("redirects: source path already has a rule")

// ErrNotFound means the rule or the 404 row does not exist.
var ErrNotFound = errors.New("redirects: not found")

// ErrImportRejected means an import had invalid rows, so nothing was written.
var ErrImportRejected = errors.New("redirects: import has invalid rows")

// RuleError is a rule that cannot be saved. Its message is shown to admins.
type RuleError struct {
	Msg string
}

func (e *RuleError) Error() string { return e.Msg }

func ruleError(msg string) error { return &RuleError{Msg: msg} }

// Redirect is one row of the redirects table. Its JSON names match the columns.
type Redirect struct {
	ID           uint       `gorm:"primaryKey;column:id" json:"id"`
	FromPath     string     `gorm:"column:from_path" json:"from_path"`
	ToURL        string     `gorm:"column:to_url" json:"to_url"`
	Kind         int        `gorm:"column:kind" json:"kind"`
	IsRule       bool       `gorm:"column:is_rule" json:"is_rule"`
	KeepQuery    bool       `gorm:"column:keep_query" json:"keep_query"`
	Source       string     `gorm:"column:source" json:"source"`
	ExpiresAt    *time.Time `gorm:"column:expires_at" json:"expires_at"`
	Hits         int64      `gorm:"column:hits" json:"hits"`
	LastHitAt    *time.Time `gorm:"column:last_hit_at" json:"last_hit_at"`
	TargetStatus *int       `gorm:"column:target_status" json:"target_status"`
	CheckedAt    *time.Time `gorm:"column:checked_at" json:"checked_at"`
	CreatedBy    *uint      `gorm:"column:created_by" json:"created_by"`
	CreatedAt    time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// TableName binds Redirect to the redirects table.
func (Redirect) TableName() string { return "redirects" }

func (r Redirect) active(now time.Time) bool {
	return r.ExpiresAt == nil || r.ExpiresAt.After(now)
}

// Match returns the rule that applies to path. It ignores database errors and
// reports no rule, so use MatchContext when the caller must know about them.
func Match(path string) (*Redirect, bool) {
	rule, ok, err := MatchContext(context.Background(), path)
	if err != nil {
		return nil, false
	}
	return rule, ok
}

// MatchContext looks path up in the redirects table. An exact rule beats a
// wildcard rule, and among wildcard rules the longest prefix wins. Expired
// rules never match. A wildcard's captured text replaces $1 in ToURL.
func MatchContext(ctx context.Context, path string) (*Redirect, bool, error) {
	db := data.DB
	if db == nil {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var rows []Redirect
	err := db.WithContext(ctx).
		Where("((NOT is_rule AND lower(from_path) = lower(?)) OR is_rule) AND (expires_at IS NULL OR expires_at > now())", path).
		Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	rule, ok := resolve(rows, path, time.Now())
	if !ok {
		return nil, false, nil
	}
	return &rule, true, nil
}

// resolve picks the rule for path from rules, which may hold more than one
// candidate. It is shared by the request path, the chain test and the saving checks.
func resolve(rules []Redirect, path string, now time.Time) (Redirect, bool) {
	key := strings.ToLower(path)
	for _, r := range rules {
		if !r.IsRule && r.active(now) && strings.ToLower(r.FromPath) == key {
			return r, true
		}
	}
	var best Redirect
	bestPrefix := -1
	capture := ""
	for _, r := range rules {
		if !r.IsRule || !r.active(now) {
			continue
		}
		prefix, suffix, ok := strings.Cut(r.FromPath, "*")
		if !ok || len(path) < len(prefix)+len(suffix) ||
			!strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
			continue
		}
		if len(prefix) > bestPrefix {
			best, bestPrefix = r, len(prefix)
			capture = path[len(prefix) : len(path)-len(suffix)]
		}
	}
	if bestPrefix < 0 {
		return Redirect{}, false
	}
	best.ToURL = strings.ReplaceAll(best.ToURL, "$1", (&url.URL{Path: capture}).EscapedPath())
	return best, true
}

// Validate checks a redirect target. Accepted forms are a same-site path that
// starts with a single "/", or an absolute http(s) URL without userinfo.
// Everything else, including javascript:, data:, CRLF and a leading "$", is refused.
func Validate(to string) error {
	if to == "" {
		return ErrInvalidTarget
	}
	if hasControl(to) {
		return ErrInvalidTarget
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

// reservedPaths belong to the application. A rule may not start there, and
// requests for them are never redirected or counted as misses.
var reservedPaths = []string{
	"/", "/admin", "/api", "/install", "/login", "/auth", "/uploads", "/themes",
	"/static", "/assets", "/.well-known", "/healthz", "/robots.txt", "/llms.txt", "/feed.xml",
}

// IsReservedPath reports whether path belongs to the application itself.
func IsReservedPath(path string) bool {
	if strings.HasPrefix(path, "/sitemap") {
		return true
	}
	for _, p := range reservedPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// normalize checks one rule and fills in its defaults. It does not check
// loops or duplicates, which need the other rules.
func normalize(r *Redirect) error {
	r.FromPath = strings.TrimSpace(r.FromPath)
	r.ToURL = strings.TrimSpace(r.ToURL)
	if r.Kind == 0 {
		r.Kind = Moved
	}
	switch r.Kind {
	case Moved, Found, Gone:
	default:
		return ruleError("kind must be 301, 302 or 410")
	}
	from := r.FromPath
	if from == "/" || !strings.HasPrefix(from, "/") || utf8.RuneCountInString(from) > maxFromPathRunes {
		return ruleError("from_path must start with / and be at most 512 characters; the home page cannot be redirected")
	}
	if !utf8.ValidString(from) || hasControl(from) || strings.ContainsAny(from, "?#") {
		return ruleError("from_path cannot contain ?, #, or control characters")
	}
	if IsReservedPath(from) {
		return ruleError("from_path is used by the site itself")
	}
	stars := strings.Count(from, "*")
	switch {
	case stars > 1:
		return ruleError("from_path allows one * at most")
	case stars == 1:
		r.IsRule = true
	case r.IsRule:
		return ruleError("a wildcard rule needs one *")
	}
	if r.Kind == Gone {
		r.ToURL = ""
		return nil
	}
	if len(r.ToURL) > maxToURLBytes {
		return ruleError("to_url is too long")
	}
	if err := Validate(r.ToURL); err != nil {
		return ruleError("to_url must be a path on this site or an absolute http or https URL")
	}
	return nil
}

// checkChain refuses a rule that loops or makes a chain deeper than
// MaxChainDepth. rules are the active rules already saved, without the rule
// being checked. Wildcard targets are checked with a sample capture only.
func checkChain(rules []Redirect, cand Redirect, now time.Time) error {
	if cand.Kind == Gone {
		return nil
	}
	start := cand.ToURL
	if cand.IsRule {
		start = strings.ReplaceAll(start, "$1", "probe")
	}
	if isExternal(start) {
		return nil
	}
	set := make([]Redirect, 0, len(rules)+1)
	set = append(set, rules...)
	set = append(set, cand)

	seen := map[string]bool{}
	hops := 0
	cur := stripQuery(start)
	for {
		key := strings.ToLower(cur)
		if seen[key] {
			return ruleError("redirect loop: the chain comes back to a path it already passed")
		}
		seen[key] = true
		hops++
		if hops > MaxChainDepth {
			return ruleError("redirect chain is deeper than 5 hops")
		}
		next, ok := resolve(set, cur, now)
		if !ok || next.Kind == Gone || isExternal(next.ToURL) {
			break
		}
		cur = stripQuery(next.ToURL)
	}
	if cand.IsRule {
		return nil
	}
	if hops+incomingDepth(rules, cand.FromPath, now) > MaxChainDepth {
		return ruleError("redirect chain is deeper than 5 hops")
	}
	return nil
}

// incomingDepth returns the most hops that end at node, counting the rules
// already saved. Wildcard rules and 410 rules are not part of a chain.
func incomingDepth(rules []Redirect, node string, now time.Time) int {
	sources := map[string][]string{}
	for _, r := range rules {
		if r.IsRule || r.Kind == Gone || !r.active(now) || isExternal(r.ToURL) {
			continue
		}
		to := strings.ToLower(stripQuery(r.ToURL))
		sources[to] = append(sources[to], strings.ToLower(r.FromPath))
	}
	return depthTo(sources, strings.ToLower(node), map[string]bool{}, map[string]int{})
}

func depthTo(sources map[string][]string, node string, visiting map[string]bool, memo map[string]int) int {
	if d, ok := memo[node]; ok {
		return d
	}
	if visiting[node] {
		return 0
	}
	visiting[node] = true
	best := 0
	for _, src := range sources[node] {
		if d := 1 + depthTo(sources, src, visiting, memo); d > best {
			best = d
		}
	}
	visiting[node] = false
	memo[node] = best
	return best
}

func isExternal(to string) bool {
	lower := strings.ToLower(to)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func stripQuery(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

var (
	hitMu   sync.Mutex
	hitRows = map[string]int64{}
)

// CountHit adds one hit for the rule whose from_path is path. The count stays
// in memory until FlushHits writes it.
func CountHit(path string) {
	hitMu.Lock()
	defer hitMu.Unlock()
	hitRows[path]++
}

// FlushHits writes the pending hit counts to the redirects table. A count that
// fails to write stays pending for the next call. The server calls it every 30 seconds.
func FlushHits() error {
	hitMu.Lock()
	pending := hitRows
	hitRows = map[string]int64{}
	hitMu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	db := data.DB
	if db == nil {
		restoreHits(pending)
		return errors.New("redirects: database is not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var firstErr error
	for path, n := range pending {
		err := db.WithContext(ctx).
			Exec("UPDATE redirects SET hits = hits + ?, last_hit_at = now() WHERE from_path = ?", n, path).Error
		if err != nil {
			restoreHits(map[string]int64{path: n})
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func restoreHits(pending map[string]int64) {
	hitMu.Lock()
	defer hitMu.Unlock()
	for path, n := range pending {
		hitRows[path] += n
	}
}
