package auth

import (
	"container/list"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	loginAttemptWindow    = 60 * time.Second
	loginAttemptLimit     = 30
	loginAttemptMaxSource = 4096
)

type loginAttemptRecord struct {
	source    netip.Addr
	attempts  int
	expiresAt time.Time
}

type loginAttemptLimiter struct {
	mu      sync.Mutex
	entries map[netip.Addr]*list.Element
	expiry  list.List
}

// ProtectLoginAttempts limits local login requests by their connection source.
// Each call creates an independent limiter for the route being registered.
func ProtectLoginAttempts(next echo.HandlerFunc) echo.HandlerFunc {
	limiter := &loginAttemptLimiter{
		entries: make(map[netip.Addr]*list.Element),
	}

	return func(c echo.Context) error {
		if allowed, retryAfter := limiter.allow(loginAttemptSourceFromRemoteAddr(c.Request().RemoteAddr)); !allowed {
			c.Response().Header().Set(echo.HeaderRetryAfter, strconv.FormatInt(retryAfter, 10))
			message := "Too many login attempts. Please try again in " + strconv.FormatInt(retryAfter, 10) + " seconds."
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": message})
		}
		return next(c)
	}
}

func loginAttemptSourceFromRemoteAddr(remoteAddr string) netip.Addr {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}

	return addrPort.Addr().WithZone("").Unmap()
}

// allow counts every request that passed the outer browser-write protection.
// Retry rejections never move the original expiry time.
func (limiter *loginAttemptLimiter) allow(source netip.Addr) (bool, int64) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	limiter.removeExpired(now)

	if element, ok := limiter.entries[source]; ok {
		record := element.Value.(*loginAttemptRecord)
		if record.attempts >= loginAttemptLimit {
			return false, retryAfterSeconds(record.expiresAt, now)
		}
		record.attempts++
		return true, 0
	}

	if len(limiter.entries) >= loginAttemptMaxSource {
		oldest := limiter.expiry.Front().Value.(*loginAttemptRecord)
		return false, retryAfterSeconds(oldest.expiresAt, now)
	}

	record := &loginAttemptRecord{
		source:    source,
		attempts:  1,
		expiresAt: now.Add(loginAttemptWindow),
	}
	element := limiter.expiry.PushBack(record)
	limiter.entries[source] = element
	return true, 0
}

func (limiter *loginAttemptLimiter) removeExpired(now time.Time) {
	for element := limiter.expiry.Front(); element != nil; element = limiter.expiry.Front() {
		record := element.Value.(*loginAttemptRecord)
		if now.Before(record.expiresAt) {
			return
		}
		delete(limiter.entries, record.source)
		limiter.expiry.Remove(element)
	}
}

func retryAfterSeconds(expiresAt, now time.Time) int64 {
	remaining := expiresAt.Sub(now)
	seconds := int64(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}
