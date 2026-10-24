// Package cache is the key-value cache behind page and object caching.
//
// Drivers: memory (implemented here) and redis (Redis or Valkey, stub until
// the cache settings page is wired).
package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Driver names accepted by Config.Driver.
const (
	DriverMemory = "memory"
	DriverRedis  = "redis"
)

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("cache: not implemented")

// Config selects and configures a driver. URL carries the Redis connection
// string when Driver is redis; it is stored encrypted (cache_url_enc).
type Config struct {
	Driver string
	URL    string
	// DefaultTTL applies when Set receives a zero ttl.
	DefaultTTL time.Duration
}

// Stats is a snapshot for the infrastructure page.
type Stats struct {
	Driver  string
	Entries int64
	Bytes   int64
	Hits    uint64
	Misses  uint64
}

// Store is the cache interface. Namespaces keep plugins and core features apart.
type Store interface {
	Get(ctx context.Context, ns, key string) ([]byte, bool, error)
	Set(ctx context.Context, ns, key string, v []byte, ttl time.Duration) error
	Delete(ctx context.Context, ns string, keys ...string) error
	Flush(ctx context.Context, ns string) error
	Incr(ctx context.Context, ns, key string, ttl time.Duration) (int64, error)
	Stats(ctx context.Context) (Stats, error)
	Ping(ctx context.Context) (time.Duration, error)
	Close() error
}

// Open returns the store for cfg. Memory is fully working; redis returns a
// stub whose operations report ErrNotImplemented.
func Open(cfg Config) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", DriverMemory:
		return newMemory(cfg.DefaultTTL), nil
	case DriverRedis:
		if cfg.URL == "" {
			return nil, errors.New("cache: redis driver needs a URL")
		}
		return &redisStore{url: cfg.URL}, nil
	default:
		return nil, fmt.Errorf("cache: unknown driver %q", cfg.Driver)
	}
}

// Namespaced returns a view of s whose namespace is prefixed with ns, so a
// plugin or feature can only address keys under its own prefix.
func Namespaced(s Store, ns string) Store {
	return &namespaced{base: s, prefix: ns}
}

type namespaced struct {
	base   Store
	prefix string
}

// full encodes the prefix and the caller's namespace unambiguously: the
// length of the prefix comes first, so prefix "p" with namespace "q/r" and
// prefix "p/q" with namespace "r" never produce the same key.
func (n *namespaced) full(ns string) string {
	return strconv.Itoa(len(n.prefix)) + ":" + n.prefix + ":" + ns
}

func (n *namespaced) Get(ctx context.Context, ns, key string) ([]byte, bool, error) {
	return n.base.Get(ctx, n.full(ns), key)
}
func (n *namespaced) Set(ctx context.Context, ns, key string, v []byte, ttl time.Duration) error {
	return n.base.Set(ctx, n.full(ns), key, v, ttl)
}
func (n *namespaced) Delete(ctx context.Context, ns string, keys ...string) error {
	return n.base.Delete(ctx, n.full(ns), keys...)
}
func (n *namespaced) Flush(ctx context.Context, ns string) error {
	return n.base.Flush(ctx, n.full(ns))
}
func (n *namespaced) Incr(ctx context.Context, ns, key string, ttl time.Duration) (int64, error) {
	return n.base.Incr(ctx, n.full(ns), key, ttl)
}
func (n *namespaced) Stats(ctx context.Context) (Stats, error) { return n.base.Stats(ctx) }
func (n *namespaced) Ping(ctx context.Context) (time.Duration, error) {
	return n.base.Ping(ctx)
}
func (n *namespaced) Close() error { return nil }
