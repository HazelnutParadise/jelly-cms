package cache

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// runStoreContract checks the behaviour every Store driver must provide.
// Namespaces are prefixed with tag so a shared backend (Redis) can be reused.
func runStoreContract(t *testing.T, s Store, tag, wantDriver string) {
	t.Helper()
	ctx := context.Background()
	ns := func(name string) string { return tag + name }

	t.Run("get miss", func(t *testing.T) {
		v, ok, err := s.Get(ctx, ns("miss"), "absent")
		if err != nil {
			t.Fatalf("Get on a missing key returned error %v", err)
		}
		if ok || v != nil {
			t.Errorf("Get on a missing key = (%q, %v), want (nil, false)", v, ok)
		}
	})

	t.Run("set then get and overwrite", func(t *testing.T) {
		if err := s.Set(ctx, ns("page"), "k1", []byte("v1"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		v, ok, err := s.Get(ctx, ns("page"), "k1")
		if err != nil || !ok || string(v) != "v1" {
			t.Fatalf("Get after Set = (%q, %v, %v), want (v1, true, nil)", v, ok, err)
		}
		if err := s.Set(ctx, ns("page"), "k1", []byte("v2"), 0); err != nil {
			t.Fatalf("overwrite Set: %v", err)
		}
		if v, _, _ := s.Get(ctx, ns("page"), "k1"); string(v) != "v2" {
			t.Errorf("Get after overwrite = %q, want v2", v)
		}
	})

	t.Run("empty value is a hit", func(t *testing.T) {
		if err := s.Set(ctx, ns("page"), "empty", []byte{}, 0); err != nil {
			t.Fatalf("Set empty: %v", err)
		}
		v, ok, err := s.Get(ctx, ns("page"), "empty")
		if err != nil || !ok || len(v) != 0 {
			t.Errorf("Get empty value = (%q, %v, %v), want an empty hit", v, ok, err)
		}
	})

	t.Run("returned bytes are copies", func(t *testing.T) {
		if err := s.Set(ctx, ns("copy"), "k", []byte("stable"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		v, _, _ := s.Get(ctx, ns("copy"), "k")
		if len(v) > 0 {
			v[0] = 'X'
		}
		again, _, _ := s.Get(ctx, ns("copy"), "k")
		if !bytes.Equal(again, []byte("stable")) {
			t.Errorf("mutating a returned slice changed the stored value to %q", again)
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := s.Set(ctx, ns("del"), "a", []byte("1"), 0); err != nil {
			t.Fatalf("Set a: %v", err)
		}
		if err := s.Set(ctx, ns("del"), "b", []byte("2"), 0); err != nil {
			t.Fatalf("Set b: %v", err)
		}
		if err := s.Delete(ctx, ns("del"), "a", "b"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for _, key := range []string{"a", "b"} {
			if _, ok, _ := s.Get(ctx, ns("del"), key); ok {
				t.Errorf("key %s still present after Delete", key)
			}
		}
	})

	t.Run("ttl expiry", func(t *testing.T) {
		if err := s.Set(ctx, ns("ttl"), "short", []byte("x"), 300*time.Millisecond); err != nil {
			t.Fatalf("Set with ttl: %v", err)
		}
		if _, ok, _ := s.Get(ctx, ns("ttl"), "short"); !ok {
			t.Fatalf("value with a 300 ms ttl was missing immediately after Set")
		}
		time.Sleep(600 * time.Millisecond)
		if _, ok, _ := s.Get(ctx, ns("ttl"), "short"); ok {
			t.Errorf("value outlived its ttl")
		}
	})

	t.Run("flush keeps other namespaces", func(t *testing.T) {
		if err := s.Set(ctx, ns("flush-a"), "k", []byte("A"), 0); err != nil {
			t.Fatalf("Set a: %v", err)
		}
		if err := s.Set(ctx, ns("flush-b"), "k", []byte("B"), 0); err != nil {
			t.Fatalf("Set b: %v", err)
		}
		if err := s.Flush(ctx, ns("flush-a")); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if _, ok, _ := s.Get(ctx, ns("flush-a"), "k"); ok {
			t.Errorf("Flush left a key in its namespace")
		}
		if v, ok, _ := s.Get(ctx, ns("flush-b"), "k"); !ok || string(v) != "B" {
			t.Errorf("Flush of one namespace removed another namespace's key: (%q, %v)", v, ok)
		}
	})

	t.Run("incr counts from one", func(t *testing.T) {
		for want := int64(1); want <= 3; want++ {
			got, err := s.Incr(ctx, ns("ctr"), "n", 0)
			if err != nil {
				t.Fatalf("Incr: %v", err)
			}
			if got != want {
				t.Errorf("Incr #%d = %d, want %d", want, got, want)
			}
		}
	})

	t.Run("incr window restarts after ttl", func(t *testing.T) {
		if got, err := s.Incr(ctx, ns("win"), "n", 300*time.Millisecond); err != nil || got != 1 {
			t.Fatalf("first Incr = (%d, %v), want (1, nil)", got, err)
		}
		time.Sleep(600 * time.Millisecond)
		if got, err := s.Incr(ctx, ns("win"), "n", 300*time.Millisecond); err != nil || got != 1 {
			t.Errorf("Incr after the window = (%d, %v), want (1, nil)", got, err)
		}
	})

	t.Run("stats count hits and misses", func(t *testing.T) {
		before, err := s.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if err := s.Set(ctx, ns("stats"), "hit", []byte("1"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		_, _, _ = s.Get(ctx, ns("stats"), "hit")
		_, _, _ = s.Get(ctx, ns("stats"), "nothing-here")
		after, err := s.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats after reads: %v", err)
		}
		if after.Hits < before.Hits+1 || after.Misses < before.Misses+1 {
			t.Errorf("Stats hits/misses went from %d/%d to %d/%d, want at least one of each more", before.Hits, before.Misses, after.Hits, after.Misses)
		}
		if after.Driver != wantDriver {
			t.Errorf("Stats.Driver = %q, want %q", after.Driver, wantDriver)
		}
	})

	t.Run("ping", func(t *testing.T) {
		if _, err := s.Ping(ctx); err != nil {
			t.Errorf("Ping: %v", err)
		}
	})
}

func TestMemoryStoreContract(t *testing.T) {
	s, err := Open(Config{Driver: DriverMemory})
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	runStoreContract(t, s, "", DriverMemory)
}

func TestNamespacedIsolation(t *testing.T) {
	ctx := context.Background()
	base, err := Open(Config{Driver: DriverMemory})
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })

	t.Run("same key in two namespaces stays separate", func(t *testing.T) {
		a := Namespaced(base, "plugin-a")
		b := Namespaced(base, "plugin-b")
		if err := a.Set(ctx, "cfg", "k", []byte("A"), 0); err != nil {
			t.Fatalf("Set via a: %v", err)
		}
		if err := b.Set(ctx, "cfg", "k", []byte("B"), 0); err != nil {
			t.Fatalf("Set via b: %v", err)
		}
		if v, ok, _ := a.Get(ctx, "cfg", "k"); !ok || string(v) != "A" {
			t.Errorf("view a reads (%q, %v), want (A, true)", v, ok)
		}
		if v, ok, _ := b.Get(ctx, "cfg", "k"); !ok || string(v) != "B" {
			t.Errorf("view b reads (%q, %v), want (B, true)", v, ok)
		}
		if _, ok, _ := base.Get(ctx, "cfg", "k"); ok {
			t.Errorf("an unprefixed read of the base store found a namespaced key")
		}
	})

	t.Run("flush through one view leaves the other", func(t *testing.T) {
		a := Namespaced(base, "flush-a")
		b := Namespaced(base, "flush-b")
		_ = a.Set(ctx, "cfg", "k", []byte("A"), 0)
		_ = b.Set(ctx, "cfg", "k", []byte("B"), 0)
		if err := a.Flush(ctx, "cfg"); err != nil {
			t.Fatalf("Flush via a: %v", err)
		}
		if _, ok, _ := a.Get(ctx, "cfg", "k"); ok {
			t.Errorf("Flush via a left its key")
		}
		if v, ok, _ := b.Get(ctx, "cfg", "k"); !ok || string(v) != "B" {
			t.Errorf("Flush via a removed b's key: (%q, %v)", v, ok)
		}
	})

	t.Run("prefixes cannot collide through a slash", func(t *testing.T) {
		// "p" with namespace "q/r" and "p/q" with namespace "r" would share the
		// base key space if the prefix and namespace were joined with a bare "/".
		p := Namespaced(base, "p")
		pq := Namespaced(base, "p/q")
		if err := p.Set(ctx, "q/r", "k", []byte("P"), 0); err != nil {
			t.Fatalf("Set via p: %v", err)
		}
		if v, ok, _ := pq.Get(ctx, "r", "k"); ok {
			t.Errorf("view p/q read p's value %q for namespace r", v)
		}
		if err := pq.Set(ctx, "r", "k", []byte("PQ"), 0); err != nil {
			t.Fatalf("Set via p/q: %v", err)
		}
		if v, ok, _ := p.Get(ctx, "q/r", "k"); !ok || string(v) != "P" {
			t.Errorf("view p lost its value to view p/q: (%q, %v)", v, ok)
		}
	})
}

func TestRedisStoreContractIfConfigured(t *testing.T) {
	url := os.Getenv("JELLY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set JELLY_TEST_REDIS_URL to run the Redis cache contract")
	}
	s, err := Open(Config{Driver: DriverRedis, URL: url})
	if err != nil {
		t.Fatalf("Open redis: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tag := "contract-" + time.Now().UTC().Format("150405.000000000") + "-"
	runStoreContract(t, s, tag, DriverRedis)
}
