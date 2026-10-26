//go:build nats

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestNATSContract(t *testing.T) {
	raw := os.Getenv("JELLY_TEST_NATS_URL")
	if raw == "" {
		t.Skip("set JELLY_TEST_NATS_URL to an isolated local NATS JetStream service")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("invalid JELLY_TEST_NATS_URL: %q", raw)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("configured NATS service unavailable at %s: %v", u.Host, err)
	}
	_ = conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q, err := Open(Config{Driver: DriverNATS, URL: raw})
	if err != nil {
		t.Fatalf("Open NATS queue: %v", err)
	}
	defer q.Close()
	inspector := q.(Inspector)
	if _, err := q.Ping(ctx); err != nil {
		t.Fatalf("NATS queue PING: %v", err)
	}

	prefix := "nats-contract-" + time.Now().UTC().Format("150405.000000000")
	queueName := prefix
	ids := []string{prefix + "-ack", prefix + "-retry", prefix + "-future", prefix + "-transfer", prefix + "-running-transfer", prefix + "-crash-recovery"}
	ackJob := Job{ID: ids[0], Queue: queueName, Kind: "contract", DedupeKey: prefix + "-dedupe", Payload: json.RawMessage(`{"contract":true}`)}
	if got, err := q.Enqueue(ctx, ackJob); err != nil || got != ids[0] {
		t.Fatalf("Enqueue first job = %q, %v", got, err)
	}
	duplicate := ackJob
	duplicate.ID = prefix + "-duplicate"
	if got, err := q.Enqueue(ctx, duplicate); err != nil || got != ids[0] {
		t.Fatalf("Enqueue duplicate = %q, %v; want existing ID %q", got, err, ids[0])
	}
	job, err := q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != ids[0] || string(job.Payload) != string(ackJob.Payload) {
		t.Fatalf("Reserve first job = %+v, %v", job, err)
	}
	if err := q.Ack(ctx, ids[0]); err != nil {
		t.Fatalf("Ack first job: %v", err)
	}
	info, err := inspector.Get(ctx, ids[0])
	if err != nil || info.State != "done" {
		t.Fatalf("Get after Ack = %+v, %v", info, err)
	}

	future := time.Now().Add(2 * time.Second).UTC()
	if _, err := q.Enqueue(ctx, Job{ID: ids[2], Queue: queueName, Kind: "contract", RunAt: future}); err != nil {
		t.Fatalf("Enqueue future job: %v", err)
	}
	got, err := q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || got != nil {
		t.Fatalf("Reserve before RunAt = %+v, %v; want no job", got, err)
	}
	if _, err := q.Enqueue(ctx, Job{ID: ids[1], Queue: queueName, Kind: "contract"}); err != nil {
		t.Fatalf("Enqueue retry job: %v", err)
	}
	job, err = q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != ids[1] {
		t.Fatalf("Reserve retry job = %+v, %v", job, err)
	}
	retryAt := time.Now().Add(120 * time.Millisecond).UTC()
	if err := q.Fail(ctx, ids[1], errors.New("retry me"), &retryAt); err != nil {
		t.Fatalf("Fail with retry: %v", err)
	}
	time.Sleep(time.Until(retryAt) + 30*time.Millisecond)
	job, err = q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != ids[1] || job.Attempts != 2 {
		t.Fatalf("Reserve retried job = %+v, %v; want attempt 2", job, err)
	}
	if err := q.Ack(ctx, ids[1]); err != nil {
		t.Fatalf("Ack retried job: %v", err)
	}

	// Simulate a crash after reserving a dedupe key but before creating the
	// corresponding job metadata record. A retry with the same ID must finish
	// the enqueue instead of returning a nil error with an empty ID.
	crashDedupe := prefix + "-crash-dedupe"
	if _, err := q.(*natsQueue).kv.Create(natsDedupeKey(crashDedupe), []byte(ids[5])); err != nil {
		t.Fatalf("seed interrupted dedupe reservation: %v", err)
	}
	crashJob := Job{ID: ids[5], Queue: queueName, Kind: "contract", DedupeKey: crashDedupe, Payload: json.RawMessage(`{"recovered":true}`)}
	if got, err := q.Enqueue(ctx, crashJob); err != nil || got != ids[5] {
		t.Fatalf("resume interrupted enqueue = %q, %v; want job ID %q", got, err, ids[5])
	}
	if info, err := inspector.Get(ctx, ids[5]); err != nil || info.State != "queued" {
		t.Fatalf("Get recovered job = %+v, %v; want queued metadata", info, err)
	}
	job, err = q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != ids[5] {
		t.Fatalf("Reserve recovered job = %+v, %v", job, err)
	}
	if err := q.Ack(ctx, ids[5]); err != nil {
		t.Fatalf("Ack recovered job: %v", err)
	}

	transfer := Job{ID: prefix + "-transfer", Queue: queueName, Kind: "contract", Payload: json.RawMessage(`{"transfer":1}`), MaxAttempts: DefaultMaxAttempts, RunAt: time.Now().Add(time.Hour).UTC()}
	transferID, err := q.Enqueue(ctx, transfer)
	if err != nil || transferID != transfer.ID {
		t.Fatalf("Enqueue transfer source = %q, %v", transferID, err)
	}
	if got, err := q.Enqueue(ctx, transfer); err != nil || got != transfer.ID {
		t.Fatalf("replay same transfer job = %q, %v; want stable ID %q", got, err, transfer.ID)
	}
	conflict := transfer
	conflict.Payload = json.RawMessage(`{"transfer":2}`)
	if got, err := q.Enqueue(ctx, conflict); err == nil {
		t.Fatalf("same job ID with different contents returned %q, want conflict", got)
	}
	transferSource, ok := q.(TransferSource)
	if !ok {
		t.Fatal("NATS queue does not implement TransferSource")
	}
	if err := transferSource.ConfirmQueued(ctx, []string{transfer.ID}); err != nil {
		t.Fatalf("ConfirmQueued first confirmation: %v", err)
	}
	if err := transferSource.ConfirmQueued(ctx, []string{transfer.ID}); err != nil {
		t.Errorf("ConfirmQueued retry for already removed ID: %v", err)
	}
	if _, err := inspector.Get(ctx, transfer.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after transfer confirmation = %v, want ErrNotFound", err)
	}

	runningID := prefix + "-running-transfer"
	if _, err := q.Enqueue(ctx, Job{ID: runningID, Queue: queueName, Kind: "contract"}); err != nil {
		t.Fatalf("Enqueue running transfer job: %v", err)
	}
	job, err = q.Reserve(ctx, []string{queueName}, prefix+"-worker", 5*time.Second)
	if err != nil || job == nil || job.ID != runningID {
		t.Fatalf("Reserve running transfer job = %+v, %v", job, err)
	}
	if err := transferSource.ConfirmQueued(ctx, []string{runningID}); err == nil {
		t.Error("ConfirmQueued removed a job that is running")
	}
	if err := q.Ack(ctx, runningID); err != nil {
		t.Fatalf("Ack running transfer job: %v", err)
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close before restart: %v", err)
	}
	q2, err := Open(Config{Driver: DriverNATS, URL: raw})
	if err != nil {
		t.Fatalf("reopen NATS queue: %v", err)
	}
	defer q2.Close()
	defer func() {
		for _, id := range ids {
			_ = q2.(Inspector).Remove(context.Background(), id)
		}
		if nq, ok := q2.(*natsQueue); ok {
			_ = nq.kv.Delete(natsDedupeKey(prefix + "-dedupe"))
			_ = nq.kv.Delete(natsDedupeKey(crashDedupe))
			_ = nq.js.DeleteConsumer(natsJobsStream, "JELLY_"+natsQueueKey(queueName))
		}
	}()
	info, err = q2.(Inspector).Get(ctx, ids[0])
	if err != nil || info.State != "done" {
		t.Fatalf("Get after client restart = %+v, %v", info, err)
	}
}
