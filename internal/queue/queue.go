// Package queue is the background job queue. Queue names: mail, media,
// webhook, import, schedule. Job kinds are registered with Register.
//
// Drivers: postgres (the jobs table, migration 0019), memory (in-process, for
// tests and as a switch target), redis (Redis Streams) and nats (JetStream,
// behind a build tag). The postgres driver lives in postgres.go; redis and
// nats are not implemented yet.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Driver names accepted by Config.Driver.
const (
	DriverPostgres = "postgres"
	DriverMemory   = "memory"
	DriverRedis    = "redis"
	DriverNATS     = "nats"
)

// Queue names used by the core features.
const (
	QueueMail     = "mail"
	QueueMedia    = "media"
	QueueWebhook  = "webhook"
	QueueImport   = "import"
	QueueSchedule = "schedule"
)

// Default retry policy: waits of 30 s, 2 min and 10 min, at most 5 attempts.
// Failed rows are kept for 7 days.
var (
	RetryBackoff       = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	DefaultMaxAttempts = 5
	FailedRetention    = 7 * 24 * time.Hour
)

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("queue: not implemented")

// ErrNotFound is returned for an unknown job ID.
var ErrNotFound = errors.New("queue: job not found")

// Config selects and configures a driver.
type Config struct {
	Driver  string
	URL     string
	Workers int
}

// Job is one unit of background work.
type Job struct {
	ID          string
	Queue       string
	Kind        string
	DedupeKey   string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
	RunAt       time.Time
}

// QueueStats counts jobs per state for one queue.
type QueueStats struct {
	Queue   string
	Queued  int
	Running int
	Done    int
	Failed  int
}

// Queue is the interface every driver implements.
type Queue interface {
	// Enqueue stores j and returns its ID. A DedupeKey that is already present
	// returns the existing ID and does not insert a second job.
	Enqueue(ctx context.Context, j Job) (string, error)
	// Reserve leases the next runnable job from queues. It returns nil, nil
	// when there is nothing to run.
	Reserve(ctx context.Context, queues []string, worker string, lease time.Duration) (*Job, error)
	Ack(ctx context.Context, id string) error
	// Fail records cause. With a retryAt the job is queued again at that time
	// while attempts remain; without one it is marked failed.
	Fail(ctx context.Context, id string, cause error, retryAt *time.Time) error
	Retry(ctx context.Context, id string) error
	Stats(ctx context.Context) ([]QueueStats, error)
	// Drain removes up to limit queued jobs and returns them. Used when
	// switching backends.
	Drain(ctx context.Context, limit int) ([]Job, error)
	Ping(ctx context.Context) (time.Duration, error)
	Close() error
}

// HandlerFunc runs one job. A returned error goes through Fail.
type HandlerFunc func(context.Context, Job) error

var (
	handlerMu sync.RWMutex
	handlers  = map[string]HandlerFunc{}
)

// Register sets the handler for kind. A later registration replaces the earlier one.
func Register(kind string, h func(context.Context, Job) error) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	handlers[kind] = h
}

// HandlerFor returns the handler registered for kind.
func HandlerFor(kind string) (HandlerFunc, bool) {
	handlerMu.RLock()
	defer handlerMu.RUnlock()
	h, ok := handlers[kind]
	return h, ok
}

// Open returns the queue for cfg. postgres (the default) and memory are
// available now; redis and nats return ErrNotImplemented.
func Open(cfg Config) (Queue, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", DriverPostgres:
		return newPostgresQueue()
	case DriverMemory:
		return newMemoryQueue(DriverMemory), nil
	case DriverRedis, DriverNATS:
		return nil, fmt.Errorf("queue: driver %q: %w", cfg.Driver, ErrNotImplemented)
	default:
		return nil, fmt.Errorf("queue: unknown driver %q", cfg.Driver)
	}
}

// switchBatch is how many queued jobs Switch moves per round.
const switchBatch = 200

// Switch moves queued jobs from one backend to another. Jobs are removed from
// from only after to has accepted them, in batches; if to refuses a job the
// rest of the batch goes back to from, so a failed switch loses nothing.
// Running jobs finish on from. Callers pause workers first so nothing new is
// reserved while the move runs.
func Switch(ctx context.Context, from, to Queue) error {
	for {
		batch, err := from.Drain(ctx, switchBatch)
		if err != nil {
			return fmt.Errorf("queue: drain source: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		for i, j := range batch {
			if _, err := to.Enqueue(ctx, j); err != nil {
				for _, back := range batch[i:] {
					if _, rerr := from.Enqueue(context.WithoutCancel(ctx), back); rerr != nil {
						err = errors.Join(err, rerr)
					}
				}
				return fmt.Errorf("queue: enqueue on target: %w", err)
			}
		}
	}
}

func newJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "job-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

type record struct {
	job        Job
	state      string // queued|running|done|failed
	lockedBy   string
	lockedTill time.Time
	lastErr    string
	finishedAt time.Time
}

// memoryQueue is the in-process queue. It is safe for concurrent use and
// forgets everything on restart.
type memoryQueue struct {
	name   string
	mu     sync.Mutex
	jobs   map[string]*record
	dedupe map[string]string
}

func newMemoryQueue(name string) *memoryQueue {
	return &memoryQueue{name: name, jobs: map[string]*record{}, dedupe: map[string]string{}}
}

func (q *memoryQueue) Enqueue(_ context.Context, j Job) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j.DedupeKey != "" {
		if id, ok := q.dedupe[j.DedupeKey]; ok {
			return id, nil
		}
	}
	if j.ID == "" {
		j.ID = newJobID()
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = DefaultMaxAttempts
	}
	if j.RunAt.IsZero() {
		j.RunAt = time.Now().UTC()
	}
	if j.Payload == nil {
		j.Payload = json.RawMessage("{}")
	}
	q.jobs[j.ID] = &record{job: j, state: "queued"}
	if j.DedupeKey != "" {
		q.dedupe[j.DedupeKey] = j.ID
	}
	return j.ID, nil
}

func (q *memoryQueue) Reserve(_ context.Context, queues []string, worker string, lease time.Duration) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now().UTC()
	wanted := map[string]bool{}
	for _, name := range queues {
		wanted[name] = true
	}
	var best *record
	for _, r := range q.jobs {
		// A running job whose lease expired is runnable again.
		runnable := (r.state == "queued" && !r.job.RunAt.After(now)) ||
			(r.state == "running" && now.After(r.lockedTill))
		if !runnable || !wanted[r.job.Queue] {
			continue
		}
		if best == nil || r.job.RunAt.Before(best.job.RunAt) {
			best = r
		}
	}
	if best == nil {
		return nil, nil
	}
	best.state = "running"
	best.lockedBy = worker
	best.lockedTill = now.Add(lease)
	best.job.Attempts++
	job := best.job
	return &job, nil
}

func (q *memoryQueue) Ack(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[id]
	if !ok {
		return ErrNotFound
	}
	r.state = "done"
	r.finishedAt = time.Now().UTC()
	r.lockedBy = ""
	return nil
}

func (q *memoryQueue) Fail(_ context.Context, id string, cause error, retryAt *time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if cause != nil {
		r.lastErr = cause.Error()
	}
	r.lockedBy = ""
	if retryAt != nil && r.job.Attempts < r.job.MaxAttempts {
		r.state = "queued"
		r.job.RunAt = retryAt.UTC()
		return nil
	}
	r.state = "failed"
	r.finishedAt = time.Now().UTC()
	return nil
}

func (q *memoryQueue) Retry(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[id]
	if !ok {
		return ErrNotFound
	}
	r.state = "queued"
	r.job.RunAt = time.Now().UTC()
	r.finishedAt = time.Time{}
	return nil
}

func (q *memoryQueue) Stats(context.Context) ([]QueueStats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	byQueue := map[string]*QueueStats{}
	for _, r := range q.jobs {
		s := byQueue[r.job.Queue]
		if s == nil {
			s = &QueueStats{Queue: r.job.Queue}
			byQueue[r.job.Queue] = s
		}
		switch r.state {
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
	out := make([]QueueStats, 0, len(byQueue))
	for _, s := range byQueue {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue < out[j].Queue })
	return out, nil
}

func (q *memoryQueue) Drain(_ context.Context, limit int) ([]Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var queued []*record
	for _, r := range q.jobs {
		if r.state == "queued" {
			queued = append(queued, r)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].job.RunAt.Before(queued[j].job.RunAt) })
	if limit > 0 && len(queued) > limit {
		queued = queued[:limit]
	}
	out := make([]Job, 0, len(queued))
	for _, r := range queued {
		out = append(out, r.job)
		delete(q.jobs, r.job.ID)
		if r.job.DedupeKey != "" {
			delete(q.dedupe, r.job.DedupeKey)
		}
	}
	return out, nil
}

func (q *memoryQueue) Ping(context.Context) (time.Duration, error) { return 0, nil }

func (q *memoryQueue) Close() error { return nil }
