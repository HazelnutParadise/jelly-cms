package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)

// postgresQueue keeps jobs in the jobs table (migration 0019). Every method
// reads data.DB when it runs, so a node that restarts or reconnects keeps
// working, and any number of nodes may share the table.
type postgresQueue struct{}

func newPostgresQueue() (Queue, error) { return &postgresQueue{}, nil }

func (q *postgresQueue) db(ctx context.Context) (*gorm.DB, error) {
	if data.DB == nil {
		return nil, errors.New("queue: database is not initialized")
	}
	return data.DB.WithContext(ctx), nil
}

type jobRow struct {
	ID          string
	Queue       string
	Kind        string
	DedupeKey   *string
	Payload     []byte
	Attempts    int
	MaxAttempts int
	RunAt       time.Time
}

func (r jobRow) job() Job {
	j := Job{
		ID:          r.ID,
		Queue:       r.Queue,
		Kind:        r.Kind,
		Payload:     json.RawMessage(r.Payload),
		Attempts:    r.Attempts,
		MaxAttempts: r.MaxAttempts,
		RunAt:       r.RunAt.UTC(),
	}
	if r.DedupeKey != nil {
		j.DedupeKey = *r.DedupeKey
	}
	return j
}

const jobColumns = `id::text AS id, queue, kind, dedupe_key, payload, attempts, max_attempts, run_at`

func (q *postgresQueue) Enqueue(ctx context.Context, j Job) (string, error) {
	db, err := q.db(ctx)
	if err != nil {
		return "", err
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = DefaultMaxAttempts
	}
	if j.RunAt.IsZero() {
		j.RunAt = time.Now().UTC()
	}
	payload := string(j.Payload)
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}
	var dedupe any
	if j.DedupeKey != "" {
		dedupe = j.DedupeKey
	}
	var id any // nil lets the table default generate one
	if uuidPattern.MatchString(j.ID) {
		id = j.ID
	}
	var got []string
	err = db.Raw(`INSERT INTO jobs (id, queue, kind, payload, attempts, max_attempts, run_at, dedupe_key)
		VALUES (COALESCE(?::uuid, gen_random_uuid()), ?, ?, ?::jsonb, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING RETURNING id::text`,
		id, j.Queue, j.Kind, payload, j.Attempts, j.MaxAttempts, j.RunAt.UTC(), dedupe).Scan(&got).Error
	if err != nil {
		return "", fmt.Errorf("queue: enqueue: %w", err)
	}
	if len(got) == 1 {
		return got[0], nil
	}
	// Nothing inserted: the dedupe key (or a reused ID) already exists.
	if j.DedupeKey != "" {
		var existing string
		if err := db.Raw(`SELECT id::text FROM jobs WHERE dedupe_key = ?`, j.DedupeKey).Scan(&existing).Error; err != nil {
			return "", fmt.Errorf("queue: look up duplicate: %w", err)
		}
		if existing != "" {
			return existing, nil
		}
	}
	if id != nil {
		// The ID was taken without a matching dedupe key; insert under a new one.
		j.ID = ""
		return q.Enqueue(ctx, j)
	}
	return "", errors.New("queue: enqueue stored nothing")
}

func (q *postgresQueue) Reserve(ctx context.Context, queues []string, worker string, lease time.Duration) (*Job, error) {
	if len(queues) == 0 {
		return nil, nil
	}
	db, err := q.db(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	// A job whose lease expired on its last allowed attempt will not run again.
	if err := db.Exec(`UPDATE jobs SET state = 'failed', finished_at = ?, locked_by = NULL,
		last_error = COALESCE(last_error, 'lease expired on the final attempt')
		WHERE state = 'running' AND locked_until < ? AND attempts >= max_attempts`, now, now).Error; err != nil {
		return nil, fmt.Errorf("queue: expire leases: %w", err)
	}
	var rows []jobRow
	err = db.Raw(`UPDATE jobs SET state = 'running', locked_by = ?, locked_until = ?, attempts = attempts + 1
		WHERE id = (
			SELECT id FROM jobs
			WHERE queue IN ? AND (
				(state = 'queued' AND run_at <= ?) OR
				(state = 'running' AND locked_until < ? AND attempts < max_attempts))
			ORDER BY run_at, created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED)
		RETURNING `+jobColumns, worker, now.Add(lease), queues, now, now).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("queue: reserve: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	job := rows[0].job()
	return &job, nil
}

func (q *postgresQueue) Ack(ctx context.Context, id string) error {
	return q.update(ctx, id, `UPDATE jobs SET state = 'done', finished_at = now(), locked_by = NULL, locked_until = NULL WHERE id = ?::uuid`)
}

func (q *postgresQueue) Fail(ctx context.Context, id string, cause error, retryAt *time.Time) error {
	db, err := q.db(ctx)
	if err != nil {
		return err
	}
	if !uuidPattern.MatchString(id) {
		return ErrNotFound
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	var runAt any
	if retryAt != nil {
		runAt = retryAt.UTC()
	}
	res := db.Exec(`UPDATE jobs SET
			state = CASE WHEN ?::timestamptz IS NOT NULL AND attempts < max_attempts THEN 'queued' ELSE 'failed' END,
			run_at = CASE WHEN ?::timestamptz IS NOT NULL AND attempts < max_attempts THEN ?::timestamptz ELSE run_at END,
			finished_at = CASE WHEN ?::timestamptz IS NOT NULL AND attempts < max_attempts THEN NULL ELSE now() END,
			locked_by = NULL, locked_until = NULL,
			last_error = COALESCE(NULLIF(?, ''), last_error)
		WHERE id = ?::uuid`, runAt, runAt, runAt, runAt, message, id)
	if res.Error != nil {
		return fmt.Errorf("queue: fail: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (q *postgresQueue) Retry(ctx context.Context, id string) error {
	return q.update(ctx, id, `UPDATE jobs SET state = 'queued', run_at = now(), finished_at = NULL, locked_by = NULL, locked_until = NULL WHERE id = ?::uuid`)
}

// update runs a statement that takes the job ID as its only argument.
func (q *postgresQueue) update(ctx context.Context, id, statement string) error {
	db, err := q.db(ctx)
	if err != nil {
		return err
	}
	if !uuidPattern.MatchString(id) {
		return ErrNotFound
	}
	res := db.Exec(statement, id)
	if res.Error != nil {
		return fmt.Errorf("queue: update job: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (q *postgresQueue) Stats(ctx context.Context) ([]QueueStats, error) {
	db, err := q.db(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Queue   string
		Queued  int
		Running int
		Done    int
		Failed  int
	}
	err = db.Raw(`SELECT queue,
			COUNT(*) FILTER (WHERE state = 'queued') AS queued,
			COUNT(*) FILTER (WHERE state = 'running') AS running,
			COUNT(*) FILTER (WHERE state = 'done') AS done,
			COUNT(*) FILTER (WHERE state = 'failed') AS failed
		FROM jobs GROUP BY queue ORDER BY queue`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("queue: stats: %w", err)
	}
	out := make([]QueueStats, 0, len(rows))
	for _, r := range rows {
		out = append(out, QueueStats{Queue: r.Queue, Queued: r.Queued, Running: r.Running, Done: r.Done, Failed: r.Failed})
	}
	return out, nil
}

// Drain deletes up to limit queued jobs, oldest first, and returns them.
func (q *postgresQueue) Drain(ctx context.Context, limit int) ([]Job, error) {
	db, err := q.db(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1 << 30
	}
	var rows []jobRow
	err = db.Raw(`DELETE FROM jobs WHERE id IN (
			SELECT id FROM jobs WHERE state = 'queued' ORDER BY run_at, created_at LIMIT ? FOR UPDATE SKIP LOCKED)
		RETURNING `+jobColumns, limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("queue: drain: %w", err)
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.job())
	}
	return out, nil
}

func (q *postgresQueue) Ping(ctx context.Context) (time.Duration, error) {
	db, err := q.db(ctx)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if err := db.Exec("SELECT 1").Error; err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// Close does nothing: the connection pool belongs to data.DB.
func (q *postgresQueue) Close() error { return nil }

// PruneFinished deletes done jobs older than doneAfter and failed jobs older
// than FailedRetention. Called by the schedule so the table does not grow
// without bound; dedupe keys of pruned jobs become reusable.
func PruneFinished(ctx context.Context, doneAfter time.Duration) (int64, error) {
	if data.DB == nil {
		return 0, errors.New("queue: database is not initialized")
	}
	now := time.Now().UTC()
	res := data.DB.WithContext(ctx).Exec(`DELETE FROM jobs
		WHERE (state = 'done' AND finished_at < ?) OR (state = 'failed' AND finished_at < ?)`,
		now.Add(-doneAfter), now.Add(-FailedRetention))
	return res.RowsAffected, res.Error
}
