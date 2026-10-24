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
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
)

// AdvisoryLockClass and AdvisoryLockKey identify the scheduler lock. The
// class matches the embedded migrations (731984201); the key is 2.
const (
	AdvisoryLockClass = 731984201
	AdvisoryLockKey   = 2
)

// Tick is how often Run looks for due schedules. The scheduler checks every
// minute slot that ended within one tick plus the longest jitter, and the
// dedupe key keeps repeated looks from enqueueing twice.
var Tick = 20 * time.Second

// ErrBadSpec is returned for a cron expression that cannot be parsed.
var ErrBadSpec = fmt.Errorf("schedule: invalid cron expression")

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

// Due returns the jobs for every schedule that fired in the minute of now, or
// within Jitter before it. RunAt is the slot plus a random delay below Jitter,
// never later than the slot plus Jitter. Calling Due again for the same slot
// returns the same DedupeKey, so a queue that honours it keeps one job.
func (s *Scheduler) Due(now time.Time) ([]queue.Job, error) {
	now = now.UTC()
	var jobs []queue.Job
	for _, sc := range s.schedules {
		spec, err := parseSpec(sc.Spec)
		if err != nil {
			return nil, fmt.Errorf("schedule %s: %w", sc.Name, err)
		}
		start := now.Truncate(time.Minute)
		for i := 0; time.Duration(i)*time.Minute <= sc.Jitter; i++ {
			slot := start.Add(-time.Duration(i) * time.Minute)
			if !spec.matches(slot) {
				continue
			}
			runAt := slot
			if sc.Jitter > 0 {
				runAt = slot.Add(time.Duration(rand.Int63n(int64(sc.Jitter) + 1)))
			}
			jobs = append(jobs, queue.Job{
				Queue:     sc.Queue,
				Kind:      sc.Kind,
				DedupeKey: DedupeKey(sc.Name, slot),
				RunAt:     runAt,
			})
		}
	}
	return jobs, nil
}

// tick enqueues everything due now.
func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	jobs, err := s.Due(now)
	if err != nil {
		log.Printf("schedule: %v", err)
		return
	}
	for _, j := range jobs {
		if _, err := s.queue.Enqueue(ctx, j); err != nil {
			log.Printf("schedule: enqueue %s: %v", j.Kind, err)
		}
	}
}

// Run elects one scheduler across nodes with a session-level advisory lock,
// then enqueues due jobs on every tick until ctx is done. A node that does not
// get the lock keeps trying so it takes over when the leader stops. Run
// returns nil when ctx ends.
func (s *Scheduler) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if data.DB == nil {
			if !sleep(ctx, time.Second) {
				return nil
			}
			continue
		}
		sqlDB, err := data.DB.DB()
		if err != nil {
			return err
		}
		conn, err := sqlDB.Conn(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("schedule: take connection: %v", err)
			if !sleep(ctx, time.Second) {
				return nil
			}
			continue
		}
		var got bool
		err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1, $2)", AdvisoryLockClass, AdvisoryLockKey).Scan(&got)
		if err != nil || !got {
			_ = conn.Close()
			if err != nil && ctx.Err() == nil {
				log.Printf("schedule: try lock: %v", err)
			}
			if !sleep(ctx, 500*time.Millisecond) {
				return nil
			}
			continue
		}
		s.lead(ctx, conn)
	}
	return nil
}

// lead ticks while it holds the lock on conn, then releases the lock.
func (s *Scheduler) lead(ctx context.Context, conn *sql.Conn) {
	defer func() {
		// The caller's context may be canceled already, so unlock on a fresh one.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", AdvisoryLockClass, AdvisoryLockKey); err != nil {
			log.Printf("schedule: release lock: %v", err)
		}
		_ = conn.Close()
	}()
	ticker := time.NewTicker(Tick)
	defer ticker.Stop()
	s.tick(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Losing the connection loses the lock; stop leading and re-elect.
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.PingContext(pingCtx)
			cancel()
			if err != nil {
				return
			}
			s.tick(ctx, now)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cronSpec holds the allowed values of each field.
type cronSpec struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

func (c cronSpec) matches(t time.Time) bool {
	if c.minute&(1<<uint(t.Minute())) == 0 || c.hour&(1<<uint(t.Hour())) == 0 || c.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	default:
		// Like cron, either day field may match when both are restricted.
		return domOK || dowOK
	}
}

// parseSpec reads "minute hour day-of-month month day-of-week". Each field is
// a comma list of "*", "n", "a-b", with an optional "/step".
func parseSpec(spec string) (cronSpec, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("%w: %q needs five fields", ErrBadSpec, spec)
	}
	var out cronSpec
	var err error
	if out.minute, _, err = parseField(fields[0], 0, 59); err != nil {
		return out, err
	}
	if out.hour, _, err = parseField(fields[1], 0, 23); err != nil {
		return out, err
	}
	if out.dom, out.domStar, err = parseField(fields[2], 1, 31); err != nil {
		return out, err
	}
	if out.month, _, err = parseField(fields[3], 1, 12); err != nil {
		return out, err
	}
	if out.dow, out.dowStar, err = parseField(fields[4], 0, 7); err != nil {
		return out, err
	}
	if out.dow&(1<<7) != 0 { // 7 also means Sunday
		out.dow |= 1
	}
	return out, nil
}

func parseField(field string, lo, hi int) (bits uint64, star bool, err error) {
	bad := func() (uint64, bool, error) {
		return 0, false, fmt.Errorf("%w: field %q", ErrBadSpec, field)
	}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if base, stepText, ok := strings.Cut(part, "/"); ok {
			n, convErr := strconv.Atoi(stepText)
			if convErr != nil || n <= 0 {
				return bad()
			}
			step, part = n, base
		}
		from, to := lo, hi
		switch {
		case part == "*":
			star = star || step == 1
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			x, e1 := strconv.Atoi(a)
			y, e2 := strconv.Atoi(b)
			if e1 != nil || e2 != nil {
				return bad()
			}
			from, to = x, y
		default:
			x, convErr := strconv.Atoi(part)
			if convErr != nil {
				return bad()
			}
			from, to = x, x
			if step > 1 { // "5/10" runs from 5 to the end
				to = hi
			}
		}
		if from < lo || to > hi || from > to {
			return bad()
		}
		for v := from; v <= to; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, star, nil
}
