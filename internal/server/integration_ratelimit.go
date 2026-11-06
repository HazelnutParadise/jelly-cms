package server

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/cache"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// Ticket 27 R4: three fixed 60-second windows aligned to the minute.
//
//	integration:<integration_id>  every key-authenticated route, after audit, before permission
//	auth_failures:<ip bucket>     counted on 401 key_invalid; over the limit, unknown keys answer 429
//	public:<ip bucket>            GET /api/v1 and GET /api/v1/openapi.json
//
// Limits come from JELLY_API_RATE_LIMIT_PER_INTEGRATION (600),
// JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP (60) and
// JELLY_API_RATE_LIMIT_PUBLIC_PER_IP (120); 0 disables a counter, anything
// unparsable keeps the default. Counters live in a memory cache.Store under the
// "ratelimit" namespace with a TTL equal to the seconds left in the window.

const (
	integrationRateLimitWindow            = time.Minute
	integrationRateLimitNamespace         = "ratelimit"
	integrationDefaultPerIntegrationLimit = 600
	integrationDefaultAuthFailureLimit    = 60
	integrationDefaultPublicLimit         = 120
	envIntegrationRateLimitPerIntegration = "JELLY_API_RATE_LIMIT_PER_INTEGRATION"
	envIntegrationRateLimitAuthFailures   = "JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP"
	envIntegrationRateLimitPublic         = "JELLY_API_RATE_LIMIT_PUBLIC_PER_IP"
)

// integrationRateLimitNow is the limiter clock. Tests replace it to move
// between windows.
var integrationRateLimitNow = time.Now

// integrationRateLimiter holds the three limits and the counter store.
type integrationRateLimiter struct {
	store          cache.Store
	perIntegration int
	authFailures   int
	public         int
	now            func() time.Time
	// mu serializes the read-modify-write of a counter. The memory store has no
	// atomic window-aware increment, so the limiter does it under this lock.
	mu sync.Mutex
}

// newIntegrationRateLimiterFromEnv reads the three limits from the environment
// and opens a memory store for the counters.
func newIntegrationRateLimiterFromEnv() *integrationRateLimiter {
	store, err := cache.Open(cache.Config{Driver: cache.DriverMemory})
	if err != nil {
		panic(fmt.Sprintf("ticket 27: open rate limit store: %v", err))
	}
	return &integrationRateLimiter{
		store:          store,
		perIntegration: integrationRateLimitFromEnv(envIntegrationRateLimitPerIntegration, integrationDefaultPerIntegrationLimit),
		authFailures:   integrationRateLimitFromEnv(envIntegrationRateLimitAuthFailures, integrationDefaultAuthFailureLimit),
		public:         integrationRateLimitFromEnv(envIntegrationRateLimitPublic, integrationDefaultPublicLimit),
		now:            func() time.Time { return integrationRateLimitNow() },
	}
}

// integrationRateLimitFromEnv parses a non-negative integer limit. Missing,
// unparsable or negative values keep def; 0 disables the counter.
func integrationRateLimitFromEnv(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// integrationIPBucket maps a client IP to its counter bucket: the whole
// address for IPv4, the /64 prefix for IPv6.
func integrationIPBucket(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

// integrationRateWindow returns the start of the minute window that holds now,
// the seconds left until it ends (at least 1, rounded up) and that start as a
// unix time.
func integrationRateWindow(now time.Time) (int64, int) {
	begin := now.Truncate(integrationRateLimitWindow)
	end := begin.Add(integrationRateLimitWindow)
	reset := int(math.Ceil(end.Sub(now).Seconds()))
	if reset < 1 {
		reset = 1
	}
	return begin.Unix(), reset
}

// counter reads the hit count of key in the window that holds now. With bump it
// adds one hit and stores the count with the window start, so a value left from
// an earlier window reads as zero. The TTL is the seconds left in the window.
func (l *integrationRateLimiter) counter(ctx context.Context, key string, now time.Time, bump bool) (int64, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	start, reset := integrationRateWindow(now)
	var count int64
	raw, ok, err := l.store.Get(ctx, integrationRateLimitNamespace, key)
	if err != nil {
		return 0, reset, err
	}
	if ok {
		if s, n, found := parseIntegrationCounter(raw); found && s == start {
			count = n
		}
	}
	if !bump {
		return count, reset, nil
	}
	count++
	value := strconv.FormatInt(start, 10) + ":" + strconv.FormatInt(count, 10)
	if err := l.store.Set(ctx, integrationRateLimitNamespace, key, []byte(value), time.Duration(reset)*time.Second); err != nil {
		return count, reset, err
	}
	return count, reset, nil
}

// parseIntegrationCounter reads a "<window start>:<count>" value.
func parseIntegrationCounter(raw []byte) (int64, int64, bool) {
	start, count, ok := strings.Cut(string(raw), ":")
	if !ok {
		return 0, 0, false
	}
	s, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	n, err := strconv.ParseInt(count, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return s, n, true
}

// writeIntegrationRateLimited answers 429 rate_limited for scope and sets the
// Retry-After and RateLimit-* headers the 429 declares.
func writeIntegrationRateLimited(c echo.Context, scope string, limit, reset int) error {
	h := c.Response().Header()
	h.Set("Retry-After", strconv.Itoa(reset))
	h.Set("RateLimit-Limit", strconv.Itoa(limit))
	h.Set("RateLimit-Remaining", "0")
	h.Set("RateLimit-Reset", strconv.Itoa(reset))
	return writeAPIError(c, http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded", map[string]any{
		"scope":          scope,
		"limit":          limit,
		"window_seconds": int(integrationRateLimitWindow / time.Second),
		"retry_after":    reset,
	})
}

// integrationAuthRejected answers a failed key check with 401 and records the
// reason in "integration.auth_failure" for limitAuthFailures. A key_invalid
// request from an IP over its auth failure limit answers 429 instead. Other
// reasons are never limited.
func integrationAuthRejected(c echo.Context, code, message string) error {
	c.Set("integration.auth_failure", code)
	if code == "key_invalid" {
		if exhausted, _ := c.Get("integration.auth_failures_exhausted").(bool); exhausted {
			limit, _ := c.Get("integration.auth_failures_limit").(int)
			reset, _ := c.Get("integration.auth_failures_reset").(int)
			return writeIntegrationRateLimited(c, "auth_failures", limit, reset)
		}
	}
	return writeAPIError(c, http.StatusUnauthorized, code, message, nil)
}

// limitAuthFailures wraps integrationKeyMiddleware. It reads the
// "integration.auth_failure" flag the key middleware sets, counts key_invalid
// failures per IP bucket and turns unknown keys into 429 once over the limit.
func (l *integrationRateLimiter) limitAuthFailures(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if l.authFailures <= 0 {
			return next(c)
		}
		ctx := c.Request().Context()
		key := "auth_failures:" + integrationIPBucket(c.RealIP())
		count, reset, err := l.counter(ctx, key, l.now(), false)
		if err != nil {
			log.Printf("rate limit auth failures: %v", err)
		} else if count >= int64(l.authFailures) {
			c.Set("integration.auth_failures_exhausted", true)
			c.Set("integration.auth_failures_limit", l.authFailures)
			c.Set("integration.auth_failures_reset", reset)
		}
		err = next(c)
		if c.Get("integration.auth_failure") == "key_invalid" {
			if _, _, cerr := l.counter(ctx, key, l.now(), true); cerr != nil {
				log.Printf("rate limit auth failures: %v", cerr)
			}
		}
		return err
	}
}

// limitPerIntegration counts every authenticated request per integration,
// sets RateLimit-Limit, RateLimit-Remaining and RateLimit-Reset before calling
// next, and answers 429 rate_limited once over the limit.
func (l *integrationRateLimiter) limitPerIntegration(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if l.perIntegration <= 0 {
			return next(c)
		}
		a, ok := c.Get("integration.identity").(*core.Integration)
		if !ok || a == nil {
			return next(c)
		}
		count, reset, err := l.counter(c.Request().Context(), fmt.Sprintf("integration:%d", a.ID), l.now(), true)
		if err != nil {
			log.Printf("rate limit integration: %v", err)
			return next(c)
		}
		limit := l.perIntegration
		remaining := int64(limit) - count
		if remaining < 0 {
			remaining = 0
		}
		h := c.Response().Header()
		h.Set("RateLimit-Limit", strconv.Itoa(limit))
		h.Set("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		h.Set("RateLimit-Reset", strconv.Itoa(reset))
		if count > int64(limit) {
			return writeIntegrationRateLimited(c, "integration", limit, reset)
		}
		return next(c)
	}
}

// limitPublic counts the two public discovery routes per IP bucket.
func (l *integrationRateLimiter) limitPublic(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if l.public <= 0 {
			return next(c)
		}
		count, reset, err := l.counter(c.Request().Context(), "public:"+integrationIPBucket(c.RealIP()), l.now(), true)
		if err != nil {
			log.Printf("rate limit public: %v", err)
			return next(c)
		}
		if count > int64(l.public) {
			return writeIntegrationRateLimited(c, "public", l.public, reset)
		}
		return next(c)
	}
}
