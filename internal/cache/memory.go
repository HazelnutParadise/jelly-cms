package cache

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	value   []byte
	expires time.Time // zero means no expiry
}

// memory is the in-process store. It is not shared between nodes.
type memory struct {
	mu         sync.Mutex
	defaultTTL time.Duration
	items      map[string]entry
	hits       uint64
	misses     uint64
}

func newMemory(defaultTTL time.Duration) *memory {
	return &memory{defaultTTL: defaultTTL, items: map[string]entry{}}
}

func mkey(ns, key string) string { return ns + "\x00" + key }

// live reports whether e is still valid and removes it when expired. Callers hold mu.
func (m *memory) live(k string, now time.Time) (entry, bool) {
	e, ok := m.items[k]
	if !ok {
		return entry{}, false
	}
	if !e.expires.IsZero() && !now.Before(e.expires) {
		delete(m.items, k)
		return entry{}, false
	}
	return e, true
}

func (m *memory) expiry(ttl time.Duration, now time.Time) time.Time {
	if ttl <= 0 {
		ttl = m.defaultTTL
	}
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}

func (m *memory) Get(_ context.Context, ns, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.live(mkey(ns, key), time.Now())
	if !ok {
		m.misses++
		return nil, false, nil
	}
	m.hits++
	return append([]byte(nil), e.value...), true, nil
}

func (m *memory) Set(_ context.Context, ns, key string, v []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.items[mkey(ns, key)] = entry{value: append([]byte(nil), v...), expires: m.expiry(ttl, now)}
	return nil
}

func (m *memory) Delete(_ context.Context, ns string, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		delete(m.items, mkey(ns, key))
	}
	return nil
}

func (m *memory) Flush(_ context.Context, ns string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := ns + "\x00"
	for k := range m.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.items, k)
		}
	}
	return nil
}

func (m *memory) Incr(_ context.Context, ns, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	k := mkey(ns, key)
	var n int64
	e, ok := m.live(k, now)
	if ok {
		n = parseCounter(e.value)
	} else {
		e.expires = m.expiry(ttl, now)
	}
	n++
	e.value = []byte(formatCounter(n))
	m.items[k] = e
	return n, nil
}

func (m *memory) Stats(_ context.Context) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Stats{Driver: DriverMemory, Hits: m.hits, Misses: m.misses}
	now := time.Now()
	for k := range m.items {
		if _, ok := m.live(k, now); ok {
			st.Entries++
			st.Bytes += int64(len(m.items[k].value))
		}
	}
	return st, nil
}

func (m *memory) Ping(context.Context) (time.Duration, error) { return 0, nil }

func (m *memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = map[string]entry{}
	return nil
}

func parseCounter(b []byte) int64 {
	var n int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func formatCounter(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
