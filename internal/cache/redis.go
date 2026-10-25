package cache

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisCachePrefix = "jelly:cache:v1:"

type redisStore struct {
	client     *redis.Client
	defaultTTL time.Duration
	hits       atomic.Uint64
	misses     atomic.Uint64
}

func newRedisStore(raw string, defaultTTL time.Duration) (Store, error) {
	opt, err := redis.ParseURL(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("cache: parse redis URL: %w", err)
	}
	client := redis.NewClient(opt)
	return &redisStore{client: client, defaultTTL: defaultTTL}, nil
}

func cacheRedisKey(ns, key string) string {
	return redisCachePrefix + base64.RawURLEncoding.EncodeToString([]byte(ns)) + ":" + base64.RawURLEncoding.EncodeToString([]byte(key))
}

func (r *redisStore) ttl(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = r.defaultTTL
	}
	return ttl
}

func (r *redisStore) Get(ctx context.Context, ns, key string) ([]byte, bool, error) {
	b, err := r.client.Get(ctx, cacheRedisKey(ns, key)).Bytes()
	if errors.Is(err, redis.Nil) {
		r.misses.Add(1)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache: redis get: %w", err)
	}
	r.hits.Add(1)
	return b, true, nil
}
func (r *redisStore) Set(ctx context.Context, ns, key string, value []byte, ttl time.Duration) error {
	if err := r.client.Set(ctx, cacheRedisKey(ns, key), value, r.ttl(ttl)).Err(); err != nil {
		return fmt.Errorf("cache: redis set: %w", err)
	}
	return nil
}
func (r *redisStore) Delete(ctx context.Context, ns string, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	redisKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		redisKeys = append(redisKeys, cacheRedisKey(ns, key))
	}
	if err := r.client.Del(ctx, redisKeys...).Err(); err != nil {
		return fmt.Errorf("cache: redis delete: %w", err)
	}
	return nil
}
func (r *redisStore) Flush(ctx context.Context, ns string) error {
	pattern := redisCachePrefix + base64.RawURLEncoding.EncodeToString([]byte(ns)) + ":*"
	iter := r.client.Scan(ctx, 0, pattern, 256).Iterator()
	keys := make([]string, 0, 256)
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= 256 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("cache: redis flush: %w", err)
			}
			keys = keys[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("cache: redis flush scan: %w", err)
	}
	if len(keys) > 0 {
		if err := r.client.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("cache: redis flush: %w", err)
		}
	}
	return nil
}

var incrScript = redis.NewScript(`local n=redis.call('INCR', KEYS[1]); if n==1 and tonumber(ARGV[1])>0 then redis.call('PEXPIRE',KEYS[1],ARGV[1]); end; return n`)

func (r *redisStore) Incr(ctx context.Context, ns, key string, ttl time.Duration) (int64, error) {
	n, err := incrScript.Run(ctx, r.client, []string{cacheRedisKey(ns, key)}, r.ttl(ttl).Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("cache: redis increment: %w", err)
	}
	return n, nil
}
func (r *redisStore) Stats(ctx context.Context) (Stats, error) {
	st := Stats{Driver: DriverRedis, Hits: r.hits.Load(), Misses: r.misses.Load()}
	iter := r.client.Scan(ctx, 0, redisCachePrefix+"*", 256).Iterator()
	for iter.Next(ctx) {
		st.Entries++
		size, err := r.client.MemoryUsage(ctx, iter.Val()).Result()
		if err == nil {
			st.Bytes += size
		} else if !strings.Contains(strings.ToLower(err.Error()), "unknown command") {
			return Stats{}, fmt.Errorf("cache: redis memory usage: %w", err)
		}
	}
	if err := iter.Err(); err != nil {
		return Stats{}, fmt.Errorf("cache: redis stats scan: %w", err)
	}
	return st, nil
}
func (r *redisStore) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := r.client.Ping(ctx).Err(); err != nil {
		return 0, fmt.Errorf("cache: redis ping: %w", err)
	}
	return time.Since(start), nil
}
func (r *redisStore) Close() error { return r.client.Close() }

var _ Store = (*redisStore)(nil)
