//go:build nats

package queue

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const natsJobsStream = "JELLY_JOBS"
const natsJobsBucket = "JELLY_JOBS_META"
const natsDoneRetention = 14 * 24 * time.Hour

type natsRecord struct {
	Job        Job       `json:"job"`
	State      string    `json:"state"`
	LastError  string    `json:"last_error,omitempty"`
	Sequence   uint64    `json:"sequence,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
type natsQueue struct {
	nc       *nats.Conn
	js       nats.JetStreamContext
	kv       nats.KeyValue
	mu       sync.Mutex
	subs     map[string]*nats.Subscription
	inflight map[string]*nats.Msg
	workers  map[string]string
}

func openNATSQueue(cfg Config) (Queue, error) {
	url := strings.TrimSpace(cfg.URL)
	nc, err := nats.Connect(url, nats.Name("jelly-cms queue"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, fmt.Errorf("queue: connect NATS: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("queue: initialize JetStream: %w", err)
	}
	if _, err := js.StreamInfo(natsJobsStream); err != nil {
		_, err = js.AddStream(&nats.StreamConfig{Name: natsJobsStream, Subjects: []string{"jelly.jobs.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, Duplicates: 2 * time.Minute})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("queue: create JetStream stream: %w", err)
		}
	}
	kv, err := js.KeyValue(natsJobsBucket)
	if err != nil {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: natsJobsBucket, History: 1, Storage: nats.FileStorage})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("queue: create JetStream metadata bucket: %w", err)
		}
	}
	q := &natsQueue{nc: nc, js: js, kv: kv, subs: map[string]*nats.Subscription{}, inflight: map[string]*nats.Msg{}, workers: map[string]string{}}
	if err := q.recoverUnpublished(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("queue: recover JetStream enqueue: %w", err)
	}
	if err := q.pruneFinished(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("queue: prune expired JetStream jobs: %w", err)
	}
	return q, nil
}
func natsQueueKey(q string) string   { return base64.RawURLEncoding.EncodeToString([]byte(q)) }
func natsRecordKey(id string) string { return "j_" + natsQueueKey(id) }
func natsDedupeKey(k string) string  { return "d_" + natsQueueKey(k) }
func natsSubject(q string) string    { return "jelly.jobs." + natsQueueKey(q) }
func (q *natsQueue) read(id string) (natsRecord, uint64, error) {
	entry, err := q.kv.Get(natsRecordKey(id))
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return natsRecord{}, 0, ErrNotFound
		}
		return natsRecord{}, 0, err
	}
	var rec natsRecord
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return rec, 0, err
	}
	return rec, entry.Revision(), nil
}
func (q *natsQueue) write(key string, rec natsRecord, rev uint64) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if rev == 0 {
		_, err = q.kv.Create(key, b)
	} else {
		_, err = q.kv.Update(key, b, rev)
	}
	return err
}

func (q *natsQueue) Enqueue(ctx context.Context, j Job) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if j.Queue == "" || j.Kind == "" {
		return "", errors.New("queue: job queue and kind are required")
	}
	runAtOmitted := j.RunAt.IsZero()
	if j.ID == "" {
		j.ID = newJobID()
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = DefaultMaxAttempts
	}
	if j.RunAt.IsZero() {
		j.RunAt = time.Now().UTC()
	}
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(j.Payload) {
		return "", errors.New("queue: invalid job payload")
	}
	if existing, _, err := q.read(j.ID); err == nil {
		if !sameEnqueue(existing.Job, j, runAtOmitted) {
			return "", fmt.Errorf("queue: job ID %q already exists with different content", j.ID)
		}
		if err := q.ensureExisting(ctx, j.ID); err != nil {
			return "", err
		}
		return j.ID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if j.DedupeKey != "" {
		entry, err := q.kv.Get(natsDedupeKey(j.DedupeKey))
		if err == nil {
			id := string(entry.Value())
			if id == j.ID {
				// A previous call may have reserved this key before creating the
				// metadata record. Continue the same idempotent operation.
				err = nats.ErrKeyNotFound
			} else {
				if e := q.ensureExisting(ctx, id); e == nil {
					return id, nil
				} else if !errors.Is(e, ErrNotFound) {
					return "", e
				}
				if e := q.kv.Delete(natsDedupeKey(j.DedupeKey), nats.LastRevision(entry.Revision())); e != nil && !errors.Is(e, nats.ErrKeyDeleted) {
					return "", fmt.Errorf("queue: clear stale NATS dedupe key: %w", e)
				}
				err = nats.ErrKeyNotFound
			}
		}
		if !errors.Is(err, nats.ErrKeyNotFound) {
			return "", err
		}
		if _, err := q.kv.Create(natsDedupeKey(j.DedupeKey), []byte(j.ID)); err != nil {
			if errors.Is(err, nats.ErrKeyExists) {
				entry, e := q.kv.Get(natsDedupeKey(j.DedupeKey))
				if e == nil {
					id := string(entry.Value())
					if id == j.ID {
						// Same-ID concurrent enqueue; metadata Create below arbitrates.
						goto dedupeReserved
					}
					if e := q.ensureExisting(ctx, id); e == nil {
						return id, nil
					} else if !errors.Is(e, ErrNotFound) {
						return "", e
					}
				}
			}
			return "", fmt.Errorf("queue: reserve NATS dedupe key: %w", err)
		}
	}
dedupeReserved:
	rec := natsRecord{Job: j, State: "queued"}
	initial, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	revision, err := q.kv.Create(natsRecordKey(j.ID), initial)
	if err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			existing, _, readErr := q.read(j.ID)
			if readErr == nil && sameEnqueue(existing.Job, j, runAtOmitted) {
				if ensureErr := q.ensureExisting(ctx, j.ID); ensureErr != nil {
					return "", ensureErr
				}
				return j.ID, nil
			}
			if readErr == nil {
				return "", fmt.Errorf("queue: job ID %q already exists with different content", j.ID)
			}
		}
		return "", fmt.Errorf("queue: reserve JetStream job metadata: %w", err)
	}
	b, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	ack, err := q.js.Publish(natsSubject(j.Queue), b, nats.Context(ctx), nats.MsgId(j.ID))
	if err != nil {
		// Publish errors can be ambiguous: retain the queued record so a retry
		// or startup recovery can reconcile the message by its stable MsgId.
		return "", fmt.Errorf("queue: publish JetStream job: %w", err)
	}
	rec.Sequence = ack.Sequence
	if err := q.write(natsRecordKey(j.ID), rec, revision); err != nil {
		return "", fmt.Errorf("queue: save JetStream metadata: %w", err)
	}
	return j.ID, nil
}

func sameEnqueue(old, incoming Job, runAtOmitted bool) bool {
	return old.ID == incoming.ID && old.Queue == incoming.Queue && old.Kind == incoming.Kind &&
		old.DedupeKey == incoming.DedupeKey && bytes.Equal(old.Payload, incoming.Payload) &&
		old.MaxAttempts == incoming.MaxAttempts && (runAtOmitted || old.RunAt.Equal(incoming.RunAt))
}

func (q *natsQueue) ensureExisting(ctx context.Context, id string) error {
	rec, rev, err := q.read(id)
	if err != nil {
		return err
	}
	if rec.State != "queued" {
		return nil
	}
	if rec.Sequence > 0 {
		if _, err := q.js.GetMsg(natsJobsStream, rec.Sequence); err == nil {
			return nil
		}
	}
	b, err := json.Marshal(rec.Job)
	if err != nil {
		return err
	}
	ack, err := q.js.Publish(natsSubject(rec.Job.Queue), b, nats.Context(ctx), nats.MsgId(rec.Job.ID))
	if err != nil {
		return fmt.Errorf("queue: restore JetStream message: %w", err)
	}
	rec.Sequence = ack.Sequence
	return q.write(natsRecordKey(id), rec, rev)
}

func (q *natsQueue) recoverUnpublished() error {
	keys, err := q.kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return err
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "j_") {
			continue
		}
		e, err := q.kv.Get(key)
		if err != nil {
			continue
		}
		var rec natsRecord
		if json.Unmarshal(e.Value(), &rec) != nil || rec.State != "queued" || rec.Sequence != 0 {
			continue
		}
		b, err := json.Marshal(rec.Job)
		if err != nil {
			return err
		}
		ack, err := q.js.Publish(natsSubject(rec.Job.Queue), b, nats.MsgId(rec.Job.ID))
		if err != nil {
			return err
		}
		rec.Sequence = ack.Sequence
		if err := q.write(key, rec, e.Revision()); err != nil {
			return err
		}
	}
	return nil
}

func (q *natsQueue) subscription(name string, lease time.Duration) (*nats.Subscription, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if s := q.subs[name]; s != nil {
		return s, nil
	}
	durable := "JELLY_" + natsQueueKey(name)
	s, err := q.js.PullSubscribe(natsSubject(name), durable, nats.BindStream(natsJobsStream), nats.AckExplicit(), nats.AckWait(lease), nats.MaxDeliver(0))
	if err != nil {
		return nil, fmt.Errorf("queue: create JetStream consumer: %w", err)
	}
	q.subs[name] = s
	return s, nil
}
func (q *natsQueue) Reserve(ctx context.Context, queues []string, worker string, lease time.Duration) (*Job, error) {
	if worker == "" {
		return nil, errors.New("queue: worker name is required")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	for _, name := range queues {
		sub, err := q.subscription(name, lease)
		if err != nil {
			return nil, err
		}
		wait := 100 * time.Millisecond
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			wait = time.Until(deadline)
			if wait <= 0 {
				return nil, ctx.Err()
			}
		}
		msgs, err := sub.Fetch(1, nats.MaxWait(wait))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("queue: fetch JetStream job: %w", err)
		}
		for _, msg := range msgs {
			var job Job
			if err := json.Unmarshal(msg.Data, &job); err != nil {
				_ = msg.Term()
				return nil, fmt.Errorf("queue: decode JetStream job: %w", err)
			}
			if job.RunAt.After(time.Now()) {
				_ = msg.NakWithDelay(time.Until(job.RunAt))
				continue
			}
			rec, rev, err := q.read(job.ID)
			if err != nil {
				_ = msg.Nak()
				return nil, fmt.Errorf("queue: read JetStream job metadata: %w", err)
			}
			// A delayed job may be delivered and deferred many times. Count only
			// executions: a queued record starts an attempt, and redelivery of a
			// running record means its prior lease expired.
			attempts := rec.Job.Attempts
			if rec.State == "queued" || rec.State == "running" {
				attempts++
			}
			if attempts > job.MaxAttempts {
				if err := msg.Term(); err != nil {
					return nil, err
				}
				rec.State = "failed"
				rec.LastError = "delivery lease expired on final attempt"
				rec.FinishedAt = time.Now().UTC()
				if err := q.write(natsRecordKey(job.ID), rec, rev); err != nil {
					return nil, fmt.Errorf("queue: mark JetStream job failed: %w", err)
				}
				continue
			}
			if lease > 0 {
				_ = msg.InProgress(nats.AckWait(lease))
			}
			q.mu.Lock()
			q.inflight[job.ID] = msg
			q.workers[job.ID] = worker
			q.mu.Unlock()
			if rec.State == "done" || rec.State == "failed" {
				_ = msg.Ack()
				continue
			}
			rec.Job.Attempts = attempts
			job = rec.Job
			rec.State = "running"
			if err := q.write(natsRecordKey(job.ID), rec, rev); err != nil {
				_ = msg.Nak()
				return nil, fmt.Errorf("queue: lease JetStream metadata: %w", err)
			}
			return &job, nil
		}
	}
	return nil, nil
}
func (q *natsQueue) message(id string) *nats.Msg {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.inflight[id]
}
func (q *natsQueue) Ack(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := q.message(id)
	if msg == nil {
		return ErrNotFound
	}
	rec, rev, err := q.read(id)
	if err != nil {
		return err
	}
	rec.State = "done"
	rec.FinishedAt = time.Now().UTC()
	if err := q.write(natsRecordKey(id), rec, rev); err != nil {
		return fmt.Errorf("queue: update JetStream job: %w", err)
	}
	if err := msg.Ack(); err != nil {
		return fmt.Errorf("queue: ack JetStream job: %w", err)
	}
	_ = q.js.DeleteMsg(natsJobsStream, rec.Sequence)
	q.mu.Lock()
	delete(q.inflight, id)
	delete(q.workers, id)
	q.mu.Unlock()
	return nil
}
func (q *natsQueue) Fail(ctx context.Context, id string, cause error, retryAt *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := q.message(id)
	if msg == nil {
		return ErrNotFound
	}
	rec, rev, err := q.read(id)
	if err != nil {
		return err
	}
	if cause != nil {
		rec.LastError = cause.Error()
	}
	delay := time.Duration(0)
	if retryAt != nil && rec.Job.Attempts < rec.Job.MaxAttempts {
		delay = time.Until(*retryAt)
		if delay < 0 {
			delay = 0
		}
		rec.Job.RunAt = retryAt.UTC()
		rec.State = "queued"
	} else {
		rec.State = "failed"
		rec.FinishedAt = time.Now().UTC()
	}
	q.mu.Lock()
	delete(q.inflight, id)
	delete(q.workers, id)
	q.mu.Unlock()
	if err := q.write(natsRecordKey(id), rec, rev); err != nil {
		return fmt.Errorf("queue: update JetStream job: %w", err)
	}
	if retryAt != nil && rec.Job.Attempts < rec.Job.MaxAttempts {
		if err := msg.NakWithDelay(delay); err != nil {
			return err
		}
	} else if err := msg.Ack(); err != nil {
		return err
	} else {
		_ = q.js.DeleteMsg(natsJobsStream, rec.Sequence)
	}
	q.mu.Lock()
	delete(q.inflight, id)
	q.mu.Unlock()
	return nil
}
func (q *natsQueue) Retry(ctx context.Context, id string) error {
	rec, rev, err := q.read(id)
	if err != nil {
		return err
	}
	rec.Job.Attempts = 0
	rec.Job.RunAt = time.Now().UTC()
	rec.State = "queued"
	rec.FinishedAt = time.Time{}
	b, _ := json.Marshal(rec.Job)
	ack, err := q.js.Publish(natsSubject(rec.Job.Queue), b, nats.Context(ctx), nats.MsgId(fmt.Sprintf("%s-retry-%d", id, time.Now().UnixNano())))
	if err != nil {
		return err
	}
	rec.Sequence = ack.Sequence
	return q.write(natsRecordKey(id), rec, rev)
}
func (q *natsQueue) pruneFinished() error {
	keys, err := q.kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return err
	}
	now := time.Now()
	for _, key := range keys {
		if !strings.HasPrefix(key, "j_") {
			continue
		}
		entry, err := q.kv.Get(key)
		if err != nil {
			continue
		}
		var rec natsRecord
		if json.Unmarshal(entry.Value(), &rec) != nil || rec.FinishedAt.IsZero() {
			continue
		}
		retention := natsDoneRetention
		if rec.State == "failed" {
			retention = FailedRetention
		}
		if now.Sub(rec.FinishedAt) < retention {
			continue
		}
		if rec.Sequence > 0 {
			_ = q.js.DeleteMsg(natsJobsStream, rec.Sequence)
		}
		if err := q.kv.Delete(key, nats.LastRevision(entry.Revision())); err != nil && !errors.Is(err, nats.ErrKeyDeleted) {
			return err
		}
		if rec.Job.DedupeKey != "" {
			_ = q.kv.Delete(natsDedupeKey(rec.Job.DedupeKey))
		}
	}
	return nil
}
func (q *natsQueue) Stats(ctx context.Context) ([]QueueStats, error) {
	rows, err := q.list(ctx, ListFilter{}, false)
	if err != nil {
		return nil, err
	}
	by := map[string]*QueueStats{}
	for _, r := range rows {
		s := by[r.Queue]
		if s == nil {
			s = &QueueStats{Queue: r.Queue}
			by[r.Queue] = s
		}
		switch r.State {
		case "queued":
			s.Queued++
		case "running":
			s.Running++
		case "done":
			s.Done++
		case "failed":
			s.Failed++
		}
	}
	out := make([]QueueStats, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue < out[j].Queue })
	return out, nil
}
func (q *natsQueue) SnapshotQueued(ctx context.Context, limit int) ([]Job, error) {
	keys, err := q.kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, err
	}
	out := make([]Job, 0)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(key, "j_") {
			continue
		}
		e, err := q.kv.Get(key)
		if err != nil {
			continue
		}
		var rec natsRecord
		if json.Unmarshal(e.Value(), &rec) == nil && rec.State == "queued" {
			out = append(out, rec.Job)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt.Before(out[j].RunAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (q *natsQueue) ConfirmQueued(ctx context.Context, ids []string) error {
	for _, id := range ids {
		rec, rev, err := q.read(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if rec.State != "queued" {
			return fmt.Errorf("queue: job %s changed state during transfer", id)
		}
		if rec.Sequence > 0 {
			if err := q.js.DeleteMsg(natsJobsStream, rec.Sequence); err != nil && !errors.Is(err, nats.ErrMsgNotFound) {
				return fmt.Errorf("queue: remove transferred JetStream message: %w", err)
			}
		}
		if err := q.kv.Delete(natsRecordKey(id), nats.LastRevision(rev)); err != nil {
			return err
		}
		if rec.Job.DedupeKey != "" {
			_ = q.kv.Delete(natsDedupeKey(rec.Job.DedupeKey))
		}
	}
	return nil
}
func (q *natsQueue) Drain(ctx context.Context, limit int) ([]Job, error) {
	jobs, err := q.SnapshotQueued(ctx, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	if err := q.ConfirmQueued(ctx, ids); err != nil {
		return nil, err
	}
	return jobs, nil
}
func (q *natsQueue) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := q.nc.FlushWithContext(ctx); err != nil {
		return 0, err
	}
	if !q.nc.IsConnected() {
		return 0, errors.New("queue: NATS is disconnected")
	}
	return time.Since(start), nil
}
func (q *natsQueue) Close() error { return q.nc.Drain() }
func (q *natsQueue) Get(ctx context.Context, id string) (JobInfo, error) {
	r, _, err := q.read(id)
	if err != nil {
		return JobInfo{}, err
	}
	return JobInfo{Job: r.Job, State: r.State, LastError: r.LastError}, nil
}
func (q *natsQueue) List(ctx context.Context, f ListFilter) ([]JobInfo, error) {
	return q.list(ctx, f, true)
}
func (q *natsQueue) list(ctx context.Context, f ListFilter, limit bool) ([]JobInfo, error) {
	if err := q.pruneFinished(); err != nil {
		return nil, err
	}
	keys, err := q.kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, err
	}
	out := make([]JobInfo, 0)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(key, "j_") {
			continue
		}
		e, err := q.kv.Get(key)
		if err != nil {
			continue
		}
		var r natsRecord
		if json.Unmarshal(e.Value(), &r) != nil {
			continue
		}
		if f.Queue != "" && f.Queue != r.Job.Queue || f.State != "" && f.State != r.State {
			continue
		}
		out = append(out, JobInfo{Job: r.Job, State: r.State, LastError: r.LastError})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt.After(out[j].RunAt) })
	if limit && len(out) > f.limit() {
		out = out[:f.limit()]
	}
	return out, nil
}
func (q *natsQueue) RetryFailed(ctx context.Context, name string) (int64, error) {
	rows, err := q.list(ctx, ListFilter{Queue: name, State: "failed"}, false)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range rows {
		if err := q.Retry(ctx, r.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
func (q *natsQueue) Remove(ctx context.Context, id string) error {
	r, rev, err := q.read(id)
	if err != nil {
		return err
	}
	if r.State == "running" {
		return ErrJobRunning
	}
	if err := q.js.DeleteMsg(natsJobsStream, r.Sequence); err != nil {
		return err
	}
	return q.kv.Delete(natsRecordKey(id), nats.LastRevision(rev))
}

var _ Queue = (*natsQueue)(nil)
var _ Inspector = (*natsQueue)(nil)
var _ TransferSource = (*natsQueue)(nil)

func (q *natsQueue) ExtendLease(ctx context.Context, id, worker string, lease time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease <= 0 {
		return errors.New("queue: lease duration must be positive")
	}
	msg := q.message(id)
	if msg == nil {
		return ErrNotFound
	}
	q.mu.Lock()
	owner := q.workers[id]
	q.mu.Unlock()
	if owner != worker {
		return errors.New("queue: lease is no longer owned by worker")
	}
	if err := msg.InProgress(nats.AckWait(lease)); err != nil {
		return fmt.Errorf("queue: extend JetStream lease: %w", err)
	}
	return nil
}

var _ LeaseExtender = (*natsQueue)(nil)
