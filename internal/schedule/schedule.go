// Package schedule enqueues recurring work. Exactly one scheduler node
// enqueues at a time: it takes the PostgreSQL advisory lock
// pg_try_advisory_lock(731984201, 2) and every enqueue carries a DedupeKey of
// the form "name:slot", so a duplicate trigger never runs a job twice.
//
// Scheduled work includes update checks, log pruning, backups, redirect hit
// flushes, store refresh, trash purge and notification pruning.
package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/queue"
)

// AdvisoryLockClass and AdvisoryLockKey identify the scheduler lock. The
// class matches the embedded migrations (731984201); the key is 2.
const (
	AdvisoryLockClass = 731984201
	AdvisoryLockKey   = 2
)

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("schedule: not implemented")

// Schedule is one recurring job.
type Schedule struct {
	// Name is unique and forms the first half of the dedupe key.
	Name string
	// Spec is a five-field cron expression evaluated in UTC.
	Spec  string
	Queue string
	Kind  string
	// Jitter spreads firings of the same schedule across nodes.
	Jitter time.Duration
}

// Scheduler holds the schedules and the queue they enqueue into.
type Scheduler struct {
	queue     queue.Queue
	schedules []Schedule
}

// New returns a scheduler that enqueues into q.
func New(q queue.Queue, schedules ...Schedule) *Scheduler {
	return &Scheduler{queue: q, schedules: append([]Schedule(nil), schedules...)}
}

// DedupeKey returns "name:slot" where slot is the UTC minute of the firing.
func DedupeKey(name string, slot time.Time) string {
	return name + ":" + slot.UTC().Format("20060102T1504Z")
}

// Due returns the jobs whose schedules fire at or before now, ready to enqueue.
// TODO(08a): parse Spec and compute slots.
func (s *Scheduler) Due(now time.Time) ([]queue.Job, error) {
	return nil, ErrNotImplemented
}

// Run takes the advisory lock and enqueues due jobs until ctx is done.
// TODO(08a): implement the lock loop and the tick.
func (s *Scheduler) Run(ctx context.Context) error {
	return ErrNotImplemented
}
