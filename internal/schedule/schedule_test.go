package schedule

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"gorm.io/gorm"
)

// fakeQueue honours DedupeKey the way the contract requires, so a duplicate
// enqueue from a second scheduler collapses to one job.
type fakeQueue struct {
	mu     sync.Mutex
	seq    int
	jobs   map[string]queue.Job
	dedupe map[string]string
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{jobs: map[string]queue.Job{}, dedupe: map[string]string{}}
}

func (f *fakeQueue) Enqueue(_ context.Context, j queue.Job) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j.DedupeKey != "" {
		if id, ok := f.dedupe[j.DedupeKey]; ok {
			return id, nil
		}
	}
	f.seq++
	j.ID = fmt.Sprintf("fake-%d", f.seq)
	f.jobs[j.ID] = j
	if j.DedupeKey != "" {
		f.dedupe[j.DedupeKey] = j.ID
	}
	return j.ID, nil
}

func (f *fakeQueue) Reserve(context.Context, []string, string, time.Duration) (*queue.Job, error) {
	return nil, nil
}
func (f *fakeQueue) Ack(context.Context, string) error                     { return nil }
func (f *fakeQueue) Fail(context.Context, string, error, *time.Time) error { return nil }
func (f *fakeQueue) Retry(context.Context, string) error                   { return nil }
func (f *fakeQueue) Drain(context.Context, int) ([]queue.Job, error)       { return nil, nil }
func (f *fakeQueue) Ping(context.Context) (time.Duration, error)           { return 0, nil }
func (f *fakeQueue) Close() error                                          { return nil }

func (f *fakeQueue) Stats(context.Context) ([]queue.QueueStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	byQueue := map[string]*queue.QueueStats{}
	for _, j := range f.jobs {
		s := byQueue[j.Queue]
		if s == nil {
			s = &queue.QueueStats{Queue: j.Queue}
			byQueue[j.Queue] = s
		}
		s.Queued++
	}
	var out []queue.QueueStats
	for _, s := range byQueue {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeQueue) queued(t *testing.T, name string) int {
	t.Helper()
	stats, err := f.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	for _, s := range stats {
		if s.Queue == name {
			return s.Queued
		}
	}
	return 0
}

func TestScheduleDedupesPerSlot(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()
	spec := Schedule{Name: "logs.prune", Spec: "0 3 * * *", Queue: queue.QueueSchedule, Kind: "logs.prune"}
	slot := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)

	// Two nodes tick the same slot; each enqueues whatever it finds due.
	first, second := New(q, spec), New(q, spec)
	for _, s := range []*Scheduler{first, second} {
		jobs, err := s.Due(slot)
		if err != nil {
			t.Fatalf("Due(%s): %v", slot, err)
		}
		if len(jobs) != 1 {
			t.Fatalf("Due(%s) returned %d jobs, want 1", slot, len(jobs))
		}
		for _, j := range jobs {
			if want := DedupeKey("logs.prune", slot); j.DedupeKey != want {
				t.Errorf("job DedupeKey=%q, want %q", j.DedupeKey, want)
			}
			if _, err := q.Enqueue(ctx, j); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}
	}
	if got := q.queued(t, queue.QueueSchedule); got != 1 {
		t.Errorf("two schedulers on one slot queued %d jobs, want 1", got)
	}

	// The next day's slot is a new key and must run.
	next := slot.Add(24 * time.Hour)
	jobs, err := first.Due(next)
	if err != nil {
		t.Fatalf("Due(%s): %v", next, err)
	}
	for _, j := range jobs {
		if _, err := q.Enqueue(ctx, j); err != nil {
			t.Fatalf("Enqueue next slot: %v", err)
		}
	}
	if got := q.queued(t, queue.QueueSchedule); got != 2 {
		t.Errorf("after the next slot the queue holds %d jobs, want 2", got)
	}
}

func TestJitterWithinBounds(t *testing.T) {
	const jitter = 30 * time.Second
	slot := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	q := newFakeQueue()

	for i := 0; i < 50; i++ {
		// Each fresh scheduler stands for another node with its own draw.
		s := New(q, Schedule{Name: "jitter.probe", Spec: "0 3 * * *", Queue: queue.QueueSchedule, Kind: "jitter.probe", Jitter: jitter})
		jobs, err := s.Due(slot.Add(jitter))
		if err != nil {
			t.Fatalf("Due: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("Due returned %d jobs, want 1", len(jobs))
		}
		runAt := jobs[0].RunAt
		if runAt.Before(slot) || runAt.After(slot.Add(jitter)) {
			t.Fatalf("run %d RunAt=%v outside [%v, %v]", i, runAt, slot, slot.Add(jitter))
		}
	}
}

// schedulerTestDatabase creates an isolated, migrated database and points
// data.DB at it. The scheduler takes its advisory lock on that handle.
func schedulerTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run the scheduler election test")
	}
	const (
		host, port, user, pass, adminDB = "127.0.0.1", "55432", "jelly_test", "jelly-local-acceptance", "jelly_acceptance"
	)
	admin, err := data.Open(config.DatabaseConfig{Host: host, Port: port, User: user, Password: pass, Name: adminDB, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	dbName := fmt.Sprintf("jelly_schedule_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated scheduler database: %v", err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, "jelly_schedule_test_") {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated scheduler database %s: %v", dbName, err)
		}
	})
	db, err := data.Open(config.DatabaseConfig{Host: host, Port: port, User: user, Password: pass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated scheduler database: %v", err)
	}
	previous := data.DB
	data.DB = db
	t.Cleanup(func() {
		data.DB = previous
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated scheduler database: %v", err)
	}
	return db
}

func TestSingleSchedulerElected(t *testing.T) {
	db := schedulerTestDatabase(t)
	ctx := context.Background()

	q, err := queue.Open(queue.Config{Driver: queue.DriverPostgres})
	if err != nil {
		t.Fatalf("open postgres queue: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	spec := Schedule{Name: "trash.purge", Spec: "*/5 * * * *", Queue: queue.QueueSchedule, Kind: "trash.purge"}

	// The probe holds its own connection, so a try-lock from it reflects the
	// lock held by whichever scheduler is running.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("get probe connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// held reports whether another session holds the scheduler lock. When the
	// lock is free the probe takes it and releases it again, so it never keeps it.
	held := func() bool {
		t.Helper()
		var got bool
		if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT pg_try_advisory_lock(%d, %d)", AdvisoryLockClass, AdvisoryLockKey)).Scan(&got); err != nil {
			t.Fatalf("probe advisory lock: %v", err)
		}
		if got {
			if _, err := conn.ExecContext(ctx, fmt.Sprintf("SELECT pg_advisory_unlock(%d, %d)", AdvisoryLockClass, AdvisoryLockKey)); err != nil {
				t.Fatalf("release probe advisory lock: %v", err)
			}
		}
		return !got
	}
	// waitFor polls until held() has the wanted value or the deadline passes.
	waitFor := func(want bool) bool {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if held() == want {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}

	ctxA, cancelA := context.WithCancel(ctx)
	defer cancelA()
	errA := make(chan error, 1)
	go func() { errA <- New(q, spec).Run(ctxA) }()
	if !waitFor(true) {
		t.Fatalf("first scheduler never took the scheduler advisory lock")
	}

	ctxB, cancelB := context.WithCancel(ctx)
	defer cancelB()
	errB := make(chan error, 1)
	go func() { errB <- New(q, spec).Run(ctxB) }()
	time.Sleep(300 * time.Millisecond)
	if !held() {
		t.Fatalf("the lock was free while the first scheduler was running")
	}
	select {
	case err := <-errB:
		if err != nil {
			t.Fatalf("passive scheduler returned %v while the first one held the lock", err)
		}
	default:
	}

	// When the leader stops, the passive node takes over.
	cancelA()
	select {
	case <-errA:
	case <-time.After(5 * time.Second):
		t.Fatalf("first scheduler did not stop after its context was canceled")
	}
	if !waitFor(true) {
		t.Errorf("passive scheduler did not take the lock after the leader stopped")
	}
	cancelB()
	select {
	case <-errB:
	case <-time.After(5 * time.Second):
		t.Errorf("second scheduler did not stop after its context was canceled")
	}
}
