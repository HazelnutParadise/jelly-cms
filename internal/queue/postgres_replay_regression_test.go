package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPostgresReplayRegressionExplicitIDIsIdempotent(t *testing.T) {
	db := openQueueTestDatabase(t)
	q := &postgresQueue{}
	ctx := context.Background()
	runAt := time.Date(2026, 10, 26, 3, 4, 5, 123456789, time.UTC)
	original := Job{
		ID:          "b350f9bf-9a1e-4a31-b2cf-6cc8098cc2a1",
		Queue:       "mail",
		Kind:        "mail.send",
		Payload:     json.RawMessage(`{"n":1,"label":"replay"}`),
		Attempts:    2,
		MaxAttempts: 5,
		RunAt:       runAt,
	}

	id, err := q.Enqueue(ctx, original)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if id != original.ID {
		t.Fatalf("first Enqueue ID = %q, want explicit ID %q", id, original.ID)
	}

	replayedID, err := q.Enqueue(ctx, original)
	if err != nil {
		t.Fatalf("replayed Enqueue: %v", err)
	}
	if replayedID != original.ID {
		t.Errorf("replayed Enqueue ID = %q, want original ID %q", replayedID, original.ID)
	}
	semanticReplay := original
	semanticReplay.Payload = json.RawMessage("{ \"label\": \"replay\", \"n\" : 1 }")
	semanticReplayID, err := q.Enqueue(ctx, semanticReplay)
	if err != nil {
		t.Fatalf("replay with equivalent JSON object: %v", err)
	}
	if semanticReplayID != original.ID {
		t.Errorf("equivalent JSON replay ID = %q, want original ID %q", semanticReplayID, original.ID)
	}
	if got := postgresQueueJobCount(t, db); got != 1 {
		t.Errorf("job row count after replay = %d, want 1", got)
	}

	conflicting := []struct {
		name string
		edit func(*Job)
	}{
		{name: "payload", edit: func(j *Job) { j.Payload = json.RawMessage(`{"n":2,"label":"replay"}`) }},
		{name: "queue", edit: func(j *Job) { j.Queue = "webhook" }},
		{name: "kind", edit: func(j *Job) { j.Kind = "mail.other" }},
		{name: "run at", edit: func(j *Job) { j.RunAt = runAt.Add(time.Minute) }},
		{name: "attempt limits", edit: func(j *Job) { j.MaxAttempts = 6 }},
		{name: "dedupe key", edit: func(j *Job) { j.DedupeKey = "mail.send:new-key" }},
	}
	for _, tc := range conflicting {
		t.Run("conflicting "+tc.name, func(t *testing.T) {
			job := original
			tc.edit(&job)
			gotID, err := q.Enqueue(ctx, job)
			if err == nil {
				t.Errorf("Enqueue with reused ID and conflicting %s returned ID %q, want error", tc.name, gotID)
			}
			if got := postgresQueueJobCount(t, db); got != 1 {
				t.Errorf("job row count after conflicting replay = %d, want 1", got)
			}
		})
	}
}

func TestPostgresReplayRegressionZeroRunAtAndMutableAttempts(t *testing.T) {
	db := openQueueTestDatabase(t)
	q := &postgresQueue{}
	ctx := context.Background()
	original := Job{
		ID:      "e8811f28-4361-4e98-bb86-cdd0cf0388b4",
		Queue:   "mail",
		Kind:    "mail.send",
		Payload: json.RawMessage(`{"recipient":"test@example.test"}`),
		// RunAt and MaxAttempts use defaults. Replaying must not make their new
		// default values look like a conflicting immutable job identity.
	}
	firstID, err := q.Enqueue(ctx, original)
	if err != nil {
		t.Fatalf("first Enqueue with default RunAt: %v", err)
	}
	secondID, err := q.Enqueue(ctx, original)
	if err != nil {
		t.Fatalf("replay with default RunAt: %v", err)
	}
	if firstID != original.ID || secondID != original.ID {
		t.Errorf("zero-RunAt enqueue IDs = %q, %q; want original %q", firstID, secondID, original.ID)
	}
	job, err := q.Reserve(ctx, []string{"mail"}, "worker", time.Minute)
	if err != nil || job == nil || job.ID != original.ID || job.Attempts != 1 {
		t.Fatalf("Reserve after replay = (%+v, %v), want original job at attempt 1", job, err)
	}
	thirdID, err := q.Enqueue(ctx, original)
	if err != nil {
		t.Fatalf("replay after Reserve changed mutable attempt count: %v", err)
	}
	if thirdID != original.ID {
		t.Errorf("post-Reserve replay ID = %q, want original ID %q", thirdID, original.ID)
	}
	if got := postgresQueueJobCount(t, db); got != 1 {
		t.Errorf("job row count after replay around Reserve = %d, want 1", got)
	}
}

func TestPostgresReplayRegressionDedupeKeyRemainsStable(t *testing.T) {
	db := openQueueTestDatabase(t)
	q := &postgresQueue{}
	ctx := context.Background()
	job := Job{
		Queue:       "mail",
		Kind:        "mail.send",
		Payload:     json.RawMessage(`{"n":1}`),
		MaxAttempts: 5,
		DedupeKey:   "mail.send:replay-regression",
	}
	firstID, err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	secondID, err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("same dedupe key Enqueue: %v", err)
	}
	if secondID != firstID {
		t.Errorf("same dedupe key returned ID %q, want original %q", secondID, firstID)
	}
	if got := postgresQueueJobCount(t, db); got != 1 {
		t.Errorf("job row count after same-key replay = %d, want 1", got)
	}

	// An explicit ID that already belongs to a different job must never be
	// treated as a dedupe hit for the row identified by DedupeKey.
	otherID := "c372087c-923a-4e2a-846d-4eea59e14e56"
	conflict := job
	conflict.ID = otherID
	conflict.DedupeKey = "mail.send:replay-regression-other"
	if _, err := q.Enqueue(ctx, conflict); err != nil {
		t.Fatalf("enqueue explicit conflicting-ID setup: %v", err)
	}
	wrongAlias := job
	wrongAlias.ID = otherID
	wrongAlias.DedupeKey = "mail.send:replay-regression"
	gotID, err := q.Enqueue(ctx, wrongAlias)
	if err == nil {
		t.Errorf("conflicting explicit ID returned ID %q for another job's dedupe key; want error", gotID)
	}
	if got := postgresQueueJobCount(t, db); got != 2 {
		t.Errorf("job row count after conflicting explicit-ID dedupe replay = %d, want 2", got)
	}
}

func postgresQueueJobCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM jobs").Scan(&count).Error; err != nil {
		t.Fatalf("count PostgreSQL queue jobs: %v", err)
	}
	return count
}
