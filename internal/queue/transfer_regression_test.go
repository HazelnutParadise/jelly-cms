package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type transferQueue struct {
	jobs     []Job
	failAt   int
	accepted []Job
	enqueueN int
	drainErr error
	onAccept func()
}

func (q *transferQueue) Enqueue(ctx context.Context, j Job) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	q.enqueueN++
	if q.failAt > 0 && q.enqueueN == q.failAt {
		return "", errors.New("target write failed")
	}
	q.jobs = append(q.jobs, j)
	q.accepted = append(q.accepted, j)
	if q.onAccept != nil {
		q.onAccept()
	}
	return j.ID, nil
}
func (q *transferQueue) Reserve(context.Context, []string, string, time.Duration) (*Job, error) {
	return nil, nil
}
func (q *transferQueue) Ack(context.Context, string) error                     { return nil }
func (q *transferQueue) Fail(context.Context, string, error, *time.Time) error { return nil }
func (q *transferQueue) Retry(context.Context, string) error                   { return nil }
func (q *transferQueue) Stats(context.Context) ([]QueueStats, error)           { return nil, nil }
func (q *transferQueue) Drain(ctx context.Context, limit int) ([]Job, error) {
	if q.drainErr != nil {
		return nil, q.drainErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit > len(q.jobs) {
		limit = len(q.jobs)
	}
	out := append([]Job(nil), q.jobs[:limit]...)
	q.jobs = append([]Job(nil), q.jobs[limit:]...)
	return out, nil
}
func (q *transferQueue) Ping(context.Context) (time.Duration, error) { return 0, nil }
func (q *transferQueue) Close() error                                { return nil }

func transferFixtures() []Job {
	return []Job{{ID: "a", Queue: QueueMail}, {ID: "b", Queue: QueueMedia}, {ID: "c", Queue: QueueSchedule}}
}

func assertTransferIDs(t *testing.T, q *transferQueue, want ...string) {
	t.Helper()
	got := make(map[string]int)
	for _, j := range q.jobs {
		got[j.ID]++
	}
	for _, id := range want {
		if got[id] != 1 {
			t.Errorf("job %s present %d times in source, want once; remaining=%v", id, got[id], q.jobs)
		}
	}
}

func TestTransferRegressionRestoresJobsAfterTargetPartialFailure(t *testing.T) {
	from := &transferQueue{jobs: transferFixtures()}
	to := &transferQueue{failAt: 2}
	err := Switch(context.Background(), from, to)
	if err == nil || !strings.Contains(err.Error(), "target write failed") {
		t.Fatalf("Switch error = %v, want target write failure", err)
	}
	assertTransferIDs(t, from, "a", "b", "c")
	if len(to.jobs) != 1 || to.jobs[0].ID != "a" {
		t.Fatalf("target accepted jobs = %v, want [a]", to.jobs)
	}
	if to.jobs[0].Payload != nil || fmt.Sprint(to.jobs[0].Queue) != QueueMail {
		t.Errorf("first transferred job was changed: %+v", to.jobs[0])
	}
}

func TestTransferRegressionRestoresJobsWhenTargetCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	from := &transferQueue{jobs: transferFixtures()}
	to := &transferQueue{}
	to.failAt = 2
	// Cancel after target has accepted one job; Switch must requeue the rest
	// using a context that is not already canceled.
	to.onAccept = cancel
	err := Switch(ctx, from, to)
	if err == nil {
		t.Fatal("Switch with target rejection returned nil")
	}
	assertTransferIDs(t, from, "a", "b", "c")
	if len(to.jobs) != 1 || to.jobs[0].ID != "a" {
		t.Fatalf("target accepted jobs = %v, want [a]", to.jobs)
	}
}

func TestTransferRegressionDrainFailurePreservesSourceJobs(t *testing.T) {
	from := &transferQueue{jobs: transferFixtures(), drainErr: errors.New("source unavailable")}
	if err := Switch(context.Background(), from, &transferQueue{}); err == nil {
		t.Fatal("Switch error = nil, want drain error")
	}
	assertTransferIDs(t, from, "a", "b", "c")
}
