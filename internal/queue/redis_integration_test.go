package queue

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestRedisContractEnqueueReserveAckAndStats(t *testing.T) {
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
	q, err := Open(Config{Driver: DriverRedis, URL: raw})
	if err != nil {
		t.Fatalf("Open Redis queue: %v", err)
	}
	defer q.Close()
	if _, err := q.Ping(ctx); err != nil {
		t.Fatalf("Redis queue PING: %v", err)
	}
	id := "redis-contract-" + time.Now().UTC().Format("150405.000000000")
	payload := json.RawMessage(`{"contract":true}`)
	if got, err := q.Enqueue(ctx, Job{ID: id, Queue: QueueMail, Kind: "contract", DedupeKey: id, Payload: payload}); err != nil || got == "" {
		t.Fatalf("Enqueue = %q, %v", got, err)
	}
	job, err := q.Reserve(ctx, []string{QueueMail}, "contract-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != id || string(job.Payload) != string(payload) {
		t.Fatalf("Reserve = %+v, %v", job, err)
	}
	if err := q.Ack(ctx, id); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	foundDone := false
	for _, row := range stats {
		if row.Queue == QueueMail && row.Done > 0 {
			foundDone = true
		}
	}
	if !foundDone {
		t.Errorf("Stats after Ack has no completed mail job: %+v", stats)
	}
}
