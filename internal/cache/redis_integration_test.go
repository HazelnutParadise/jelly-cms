package cache

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestRedisContractRoundTripTTLNamespaceAndCounters(t *testing.T) {
	raw := os.Getenv("JELLY_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("set JELLY_TEST_REDIS_URL to an isolated local Redis/Valkey service")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("invalid JELLY_TEST_REDIS_URL: %q", raw)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("configured Redis/Valkey service unavailable at %s: %v", u.Host, err)
	}
	_ = conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Open(Config{Driver: DriverRedis, URL: raw})
	if err != nil {
		t.Fatalf("Open Redis cache: %v", err)
	}
	defer s.Close()
	if _, err := s.Ping(ctx); err != nil {
		t.Fatalf("Redis PING: %v", err)
	}
	ns := "contract-" + time.Now().UTC().Format("150405.000000000")
	defer s.Flush(context.Background(), ns)
	if err := s.Set(ctx, ns, "value", []byte("round-trip"), 20*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := s.Get(ctx, ns, "value")
	if err != nil || !ok || string(got) != "round-trip" {
		t.Fatalf("Get = %q, %v, %v", got, ok, err)
	}
	if n, err := s.Incr(ctx, ns, "counter", time.Minute); err != nil || n != 1 {
		t.Fatalf("first Incr = %d, %v; want 1", n, err)
	}
	if n, err := s.Incr(ctx, ns, "counter", time.Minute); err != nil || n != 2 {
		t.Fatalf("second Incr = %d, %v; want 2", n, err)
	}
	if err := s.Delete(ctx, ns, "value"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := s.Get(ctx, ns, "value"); err != nil || ok {
		t.Fatalf("Get after Delete = found %v, err %v", ok, err)
	}
}
