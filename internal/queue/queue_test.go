package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

const (
	queueTestHost    = "127.0.0.1"
	queueTestPort    = "55432"
	queueTestUser    = "jelly_test"
	queueTestPass    = "jelly-local-acceptance"
	queueTestAdminDB = "jelly_acceptance"
	queueTestPrefix  = "jelly_queue_test_"
)

var queueTestDBCounter atomic.Int64

// openQueueTestDatabase creates an isolated, migrated database on the dedicated
// PostgreSQL container and points data.DB at it, which is where the postgres
// queue driver keeps its jobs table.
func openQueueTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run the PostgreSQL queue contract")
	}
	admin, err := data.Open(config.DatabaseConfig{Host: queueTestHost, Port: queueTestPort, User: queueTestUser, Password: queueTestPass, Name: queueTestAdminDB, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	dbName := fmt.Sprintf("%s%d_%d", queueTestPrefix, os.Getpid(), queueTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated queue database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, queueTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated queue database %s: %v", dbName, err)
		}
	})

	db, err := data.Open(config.DatabaseConfig{Host: queueTestHost, Port: queueTestPort, User: queueTestUser, Password: queueTestPass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated queue database: %v", err)
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
		t.Fatalf("migrate isolated queue database: %v", err)
	}
	return db
}

// queueContractCases runs the behaviour every Queue driver must provide. fresh
// returns an empty queue for each case.
func queueContractCases(t *testing.T, fresh func(t *testing.T) Queue) {
	ctx := context.Background()

	t.Run("enqueue then stats", func(t *testing.T) {
		q := fresh(t)
		id, err := q.Enqueue(ctx, Job{Queue: "mail", Kind: "mail.send", Payload: json.RawMessage(`{"n":1}`)})
		if err != nil || id == "" {
			t.Fatalf("Enqueue = (%q, %v), want an ID and nil", id, err)
		}
		if got := queueStatsFor(t, q, "mail"); got.Queued != 1 {
			t.Errorf("mail stats after one enqueue: %+v, want Queued=1", got)
		}
	})

	t.Run("reserve lease and ack", func(t *testing.T) {
		q := fresh(t)
		id, _ := q.Enqueue(ctx, Job{Queue: "mail", Kind: "mail.send"})
		job, err := q.Reserve(ctx, []string{"mail"}, "worker-1", time.Minute)
		if err != nil || job == nil {
			t.Fatalf("Reserve = (%v, %v), want the queued job", job, err)
		}
		if job.ID != id || job.Attempts != 1 {
			t.Errorf("Reserve returned ID=%q Attempts=%d, want %q and 1", job.ID, job.Attempts, id)
		}
		if again, _ := q.Reserve(ctx, []string{"mail"}, "worker-2", time.Minute); again != nil {
			t.Errorf("second worker reserved %q while the lease was held", again.ID)
		}
		if err := q.Ack(ctx, id); err != nil {
			t.Fatalf("Ack: %v", err)
		}
		if got := queueStatsFor(t, q, "mail"); got.Done != 1 || got.Queued != 0 || got.Running != 0 {
			t.Errorf("mail stats after Ack: %+v, want Done=1", got)
		}
		if none, _ := q.Reserve(ctx, []string{"mail"}, "worker-1", time.Minute); none != nil {
			t.Errorf("Reserve returned %q after the only job was acked", none.ID)
		}
	})

	t.Run("expired lease is runnable again", func(t *testing.T) {
		q := fresh(t)
		id, _ := q.Enqueue(ctx, Job{Queue: "mail", Kind: "mail.send"})
		if job, _ := q.Reserve(ctx, []string{"mail"}, "crashed", 50*time.Millisecond); job == nil {
			t.Fatalf("first Reserve returned nothing")
		}
		time.Sleep(150 * time.Millisecond)
		job, err := q.Reserve(ctx, []string{"mail"}, "survivor", time.Minute)
		if err != nil || job == nil || job.ID != id {
			t.Fatalf("Reserve after lease expiry = (%v, %v), want job %s", job, err, id)
		}
	})

	t.Run("reserve only from named queues", func(t *testing.T) {
		q := fresh(t)
		id, _ := q.Enqueue(ctx, Job{Queue: "webhook", Kind: "webhook.deliver"})
		if job, _ := q.Reserve(ctx, []string{"mail"}, "w", time.Minute); job != nil {
			t.Errorf("Reserve from mail returned a webhook job %q", job.ID)
		}
		if job, _ := q.Reserve(ctx, []string{"webhook"}, "w", time.Minute); job == nil || job.ID != id {
			t.Errorf("Reserve from webhook did not return %s", id)
		}
	})

	t.Run("fail with retry waits for retryAt", func(t *testing.T) {
		q := fresh(t)
		id, _ := q.Enqueue(ctx, Job{Queue: "mail", Kind: "mail.send"})
		if job, _ := q.Reserve(ctx, []string{"mail"}, "w", time.Minute); job == nil {
			t.Fatalf("Reserve returned nothing")
		}
		retryAt := time.Now().UTC().Add(RetryBackoff[0])
		if err := q.Fail(ctx, id, errors.New("upstream timeout"), &retryAt); err != nil {
			t.Fatalf("Fail with retry: %v", err)
		}
		if got := queueStatsFor(t, q, "mail"); got.Queued != 1 || got.Failed != 0 {
			t.Errorf("mail stats after retryable Fail: %+v, want Queued=1 Failed=0", got)
		}
		if early, _ := q.Reserve(ctx, []string{"mail"}, "w", time.Minute); early != nil {
			t.Errorf("job was reservable before its retryAt")
		}
		drained, err := q.Drain(ctx, 10)
		if err != nil || len(drained) != 1 {
			t.Fatalf("Drain after retryable Fail = (%d jobs, %v), want 1", len(drained), err)
		}
		if delta := drained[0].RunAt.Sub(retryAt); delta < -2*time.Second || delta > 2*time.Second {
			t.Errorf("job RunAt=%v, want the retryAt %v", drained[0].RunAt, retryAt)
		}
	})

	t.Run("backoff is 30s 2m 10m with five attempts", func(t *testing.T) {
		if len(RetryBackoff) != 3 || RetryBackoff[0] != 30*time.Second || RetryBackoff[1] != 2*time.Minute || RetryBackoff[2] != 10*time.Minute {
			t.Fatalf("RetryBackoff = %v, want [30s 2m0s 10m0s]", RetryBackoff)
		}
		if DefaultMaxAttempts != 5 {
			t.Fatalf("DefaultMaxAttempts = %d, want 5", DefaultMaxAttempts)
		}
		q := fresh(t)
		id, _ := q.Enqueue(ctx, Job{Queue: "media", Kind: "media.thumbnail"})
		for attempt := 1; attempt <= DefaultMaxAttempts; attempt++ {
			job, err := q.Reserve(ctx, []string{"media"}, "w", time.Minute)
			if err != nil || job == nil {
				t.Fatalf("attempt %d: Reserve = (%v, %v), want the job", attempt, job, err)
			}
			if job.Attempts != attempt {
				t.Errorf("attempt %d: Attempts=%d", attempt, job.Attempts)
			}
			// Make the retry due at once so the loop can run every attempt.
			due := time.Now().UTC().Add(-time.Second)
			if err := q.Fail(ctx, id, errors.New("still failing"), &due); err != nil {
				t.Fatalf("attempt %d: Fail: %v", attempt, err)
			}
		}
		if job, _ := q.Reserve(ctx, []string{"media"}, "w", time.Minute); job != nil {
			t.Errorf("job ran a sixth time after %d attempts", DefaultMaxAttempts)
		}
		if got := queueStatsFor(t, q, "media"); got.Failed != 1 || got.Queued != 0 {
			t.Errorf("media stats after the final attempt: %+v, want Failed=1", got)
		}
	})

	t.Run("dedupe key collapses duplicates", func(t *testing.T) {
		q := fresh(t)
		first, err := q.Enqueue(ctx, Job{Queue: "media", Kind: "media.webp", DedupeKey: "logs.prune:20261023T0300Z"})
		if err != nil {
			t.Fatalf("first Enqueue: %v", err)
		}
		second, err := q.Enqueue(ctx, Job{Queue: "media", Kind: "media.webp", DedupeKey: "logs.prune:20261023T0300Z"})
		if err != nil {
			t.Fatalf("second Enqueue: %v", err)
		}
		if second != first {
			t.Errorf("duplicate dedupe key returned ID %q, want the existing %q", second, first)
		}
		if got := queueStatsFor(t, q, "media"); got.Queued != 1 {
			t.Errorf("media stats after duplicate enqueue: %+v, want Queued=1", got)
		}
	})

	t.Run("drain returns queued jobs only", func(t *testing.T) {
		q := fresh(t)
		var ids []string
		for i := 0; i < 3; i++ {
			id, _ := q.Enqueue(ctx, Job{Queue: "import", Kind: "import.attachments"})
			ids = append(ids, id)
		}
		done, _ := q.Reserve(ctx, []string{"import"}, "w", time.Minute)
		if done == nil {
			t.Fatalf("Reserve returned nothing")
		}
		if err := q.Ack(ctx, done.ID); err != nil {
			t.Fatalf("Ack: %v", err)
		}
		failed, _ := q.Reserve(ctx, []string{"import"}, "w", time.Minute)
		if failed == nil {
			t.Fatalf("second Reserve returned nothing")
		}
		if err := q.Fail(ctx, failed.ID, errors.New("bad archive"), nil); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		var remaining string
		for _, id := range ids {
			if id != done.ID && id != failed.ID {
				remaining = id
			}
		}
		drained, err := q.Drain(ctx, 10)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if len(drained) != 1 || drained[0].ID != remaining {
			got := make([]string, 0, len(drained))
			for _, j := range drained {
				got = append(got, j.ID)
			}
			t.Errorf("Drain returned %v, want only the queued job %s", got, remaining)
		}
	})

	t.Run("concurrent workers never share a job", func(t *testing.T) {
		q := fresh(t)
		const jobs, workers = 20, 4
		want := map[string]bool{}
		for i := 0; i < jobs; i++ {
			id, err := q.Enqueue(ctx, Job{Queue: "webhook", Kind: "webhook.deliver"})
			if err != nil {
				t.Fatalf("Enqueue %d: %v", i, err)
			}
			want[id] = true
		}
		var (
			mu  sync.Mutex
			got []string
			wg  sync.WaitGroup
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(worker string) {
				defer wg.Done()
				for {
					job, err := q.Reserve(ctx, []string{"webhook"}, worker, time.Minute)
					if err != nil {
						t.Errorf("worker %s Reserve: %v", worker, err)
						return
					}
					if job == nil {
						return
					}
					mu.Lock()
					got = append(got, job.ID)
					mu.Unlock()
					if err := q.Ack(ctx, job.ID); err != nil {
						t.Errorf("worker %s Ack: %v", worker, err)
					}
				}
			}(fmt.Sprintf("worker-%d", w))
		}
		wg.Wait()
		seen := map[string]int{}
		for _, id := range got {
			seen[id]++
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("job %s was reserved %d times", id, n)
			}
		}
		if len(got) != jobs {
			t.Errorf("workers reserved %d jobs, want %d", len(got), jobs)
		}
	})
}

// queueStatsFor returns the counts for one queue name.
func queueStatsFor(t *testing.T, q Queue, name string) QueueStats {
	t.Helper()
	stats, err := q.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	for _, s := range stats {
		if s.Queue == name {
			return s
		}
	}
	return QueueStats{Queue: name}
}

func TestMemoryQueueContract(t *testing.T) {
	queueContractCases(t, func(t *testing.T) Queue {
		q := newMemoryQueue("memory")
		t.Cleanup(func() { _ = q.Close() })
		return q
	})
}

func TestPostgresQueueContract(t *testing.T) {
	db := openQueueTestDatabase(t)
	fresh := func(t *testing.T) Queue {
		t.Helper()
		if err := db.Exec("DELETE FROM jobs").Error; err != nil {
			t.Fatalf("clear jobs table: %v", err)
		}
		q, err := Open(Config{Driver: DriverPostgres})
		if err != nil {
			t.Fatalf("Open postgres queue: %v", err)
		}
		t.Cleanup(func() { _ = q.Close() })
		return q
	}
	queueContractCases(t, fresh)

	t.Run("jobs survive a new instance", func(t *testing.T) {
		writer := fresh(t)
		id, err := writer.Enqueue(context.Background(), Job{Queue: "mail", Kind: "mail.send"})
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		// A restarted node opens the same database and must see the job.
		reader, err := Open(Config{Driver: DriverPostgres})
		if err != nil {
			t.Fatalf("Open second instance: %v", err)
		}
		t.Cleanup(func() { _ = reader.Close() })
		if got := queueStatsFor(t, reader, "mail"); got.Queued != 1 {
			t.Fatalf("second instance sees %+v, want the queued job %s", got, id)
		}
		if job, err := reader.Reserve(context.Background(), []string{"mail"}, "w", time.Minute); err != nil || job == nil || job.ID != id {
			t.Errorf("second instance Reserve = (%v, %v), want job %s", job, err, id)
		}
	})

	t.Run("two instances never reserve the same job", func(t *testing.T) {
		ctx := context.Background()
		seed := fresh(t)
		const jobs = 20
		for i := 0; i < jobs; i++ {
			if _, err := seed.Enqueue(ctx, Job{Queue: "webhook", Kind: "webhook.deliver"}); err != nil {
				t.Fatalf("Enqueue %d: %v", i, err)
			}
		}
		instances := make([]Queue, 4)
		for i := range instances {
			q, err := Open(Config{Driver: DriverPostgres})
			if err != nil {
				t.Fatalf("Open instance %d: %v", i, err)
			}
			t.Cleanup(func() { _ = q.Close() })
			instances[i] = q
		}
		var (
			mu  sync.Mutex
			all []string
			wg  sync.WaitGroup
		)
		for i, q := range instances {
			wg.Add(1)
			go func(q Queue, worker string) {
				defer wg.Done()
				for {
					job, err := q.Reserve(ctx, []string{"webhook"}, worker, time.Minute)
					if err != nil {
						t.Errorf("%s Reserve: %v", worker, err)
						return
					}
					if job == nil {
						return
					}
					mu.Lock()
					all = append(all, job.ID)
					mu.Unlock()
					_ = q.Ack(ctx, job.ID)
				}
			}(q, fmt.Sprintf("node-%d", i))
		}
		wg.Wait()
		sort.Strings(all)
		for i := 1; i < len(all); i++ {
			if all[i] == all[i-1] {
				t.Errorf("job %s was reserved by two instances", all[i])
			}
		}
		if len(all) != jobs {
			t.Errorf("instances reserved %d jobs, want %d", len(all), jobs)
		}
	})
}

func TestHandlersAreRegisteredByKind(t *testing.T) {
	const kind = "queue-test.registered"
	if _, ok := HandlerFor(kind); ok {
		t.Fatalf("kind %s has a handler before it was registered", kind)
	}

	sentinel := errors.New("first handler")
	Register(kind, func(context.Context, Job) error { return sentinel })
	h, ok := HandlerFor(kind)
	if !ok {
		t.Fatalf("HandlerFor(%s) found nothing after Register", kind)
	}
	if err := h(context.Background(), Job{Kind: kind}); !errors.Is(err, sentinel) {
		t.Errorf("registered handler returned %v, want the first sentinel", err)
	}

	replacement := errors.New("second handler")
	Register(kind, func(context.Context, Job) error { return replacement })
	h, _ = HandlerFor(kind)
	if err := h(context.Background(), Job{Kind: kind}); !errors.Is(err, replacement) {
		t.Errorf("re-registering kind %s did not replace the handler; returned %v", kind, err)
	}

	if _, ok := HandlerFor("queue-test.never-registered"); ok {
		t.Errorf("HandlerFor returned a handler for an unregistered kind")
	}
}
