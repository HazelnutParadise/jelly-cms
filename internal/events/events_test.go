package events

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder collects the events one subscriber receives, in order.
type recorder struct {
	names []string
}

func (r *recorder) fn(e Event) {
	r.names = append(r.names, e.Name)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// captureLog routes the standard logger into a buffer for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

// Wanted result 1: ordering, cancel and fan-out of Subscribe and Publish.
func TestSubscribePublishOrderCancelAndFanOut(t *testing.T) {
	var first, second recorder

	cancelFirst := Subscribe(first.fn)
	t.Cleanup(cancelFirst)
	cancelSecond := Subscribe(second.fn)
	t.Cleanup(cancelSecond)

	Publish(Event{Name: "e1"})
	Publish(Event{Name: "e2"})
	Publish(Event{Name: "e3"})

	want := []string{"e1", "e2", "e3"}
	if !equalStrings(first.names, want) {
		t.Fatalf("first subscriber got %v, want %v", first.names, want)
	}
	if !equalStrings(second.names, want) {
		t.Fatalf("second subscriber got %v, want %v (every subscriber gets every event)", second.names, want)
	}

	cancelFirst()
	Publish(Event{Name: "e4"})

	if !equalStrings(first.names, want) {
		t.Fatalf("canceled subscriber still received events: %v, want %v", first.names, want)
	}
	if got := second.names; !equalStrings(got, []string{"e1", "e2", "e3", "e4"}) {
		t.Fatalf("remaining subscriber got %v, want e1..e4", got)
	}
}

// Wanted result 2: a panicking subscriber is recovered and does not stop the
// others from receiving the same event.
func TestPanickingSubscriberDoesNotStopOthers(t *testing.T) {
	captureLog(t)

	cancelPanic := Subscribe(func(Event) {
		panic("subscriber exploded")
	})
	t.Cleanup(cancelPanic)

	var after recorder
	cancelAfter := Subscribe(after.fn)
	t.Cleanup(cancelAfter)

	Publish(Event{Name: "boom-1"})
	Publish(Event{Name: "boom-2"})

	if !equalStrings(after.names, []string{"boom-1", "boom-2"}) {
		t.Fatalf("subscriber after a panicking one got %v, want both events", after.names)
	}
}

// Wanted result 3: Publish fills a nil payload and a zero At.
func TestPublishFillsNilPayloadAndZeroTime(t *testing.T) {
	var got []Event
	cancel := Subscribe(func(e Event) { got = append(got, e) })
	t.Cleanup(cancel)

	before := time.Now()
	Publish(Event{Name: "Zero"})
	after := time.Now()

	if len(got) != 1 {
		t.Fatalf("subscriber received %d events, want 1", len(got))
	}
	e := got[0]
	if e.Name != "Zero" {
		t.Fatalf("Name = %q, want Zero", e.Name)
	}
	if e.Payload == nil {
		t.Fatal("Payload = nil, want an empty map")
	}
	if len(e.Payload) != 0 {
		t.Fatalf("Payload = %v, want an empty map", e.Payload)
	}
	if e.At.IsZero() {
		t.Fatal("At is zero, want the publish time")
	}
	if e.At.Location() != time.UTC {
		t.Fatalf("At location = %v, want UTC", e.At.Location())
	}
	if e.At.Before(before.Add(-2*time.Second)) || e.At.After(after.Add(2*time.Second)) {
		t.Fatalf("At = %v, want within 2s of now (%v)", e.At, time.Now())
	}

	payload := map[string]any{"k": "v"}
	Publish(Event{Name: "Kept", Payload: payload, At: time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC)})
	kept := got[len(got)-1]
	if kept.Payload["k"] != "v" {
		t.Fatalf("explicit Payload was replaced: %v", kept.Payload)
	}
	if !kept.At.Equal(time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("explicit At was replaced: %v", kept.At)
	}
}

// Wanted result 4: WithOutbox reuses an outbox already on the context.
func TestWithOutboxReusesOutermostOutbox(t *testing.T) {
	ctx1, outbox1 := WithOutbox(context.Background())
	if outbox1 == nil {
		t.Fatal("WithOutbox() returned a nil outbox")
	}

	ctx2, outbox2 := WithOutbox(ctx1)
	if outbox2 != outbox1 {
		t.Fatal("WithOutbox on a context with an outbox returned a different *Outbox")
	}
	if ctx2 != ctx1 {
		t.Fatal("WithOutbox on a context with an outbox did not return the same context")
	}

	if got := OutboxFrom(ctx1); got != outbox1 {
		t.Fatalf("OutboxFrom(ctx1) = %p, want %p", got, outbox1)
	}
	if got := OutboxFrom(ctx2); got != outbox1 {
		t.Fatalf("OutboxFrom(ctx2) = %p, want %p", got, outbox1)
	}
	if got := OutboxFrom(context.Background()); got != nil {
		t.Fatalf("OutboxFrom(background) = %p, want nil", got)
	}
}

// Wanted result 5: staged events wait for Flush, Flush publishes in order and
// empties the outbox, a second Flush publishes nothing, Discard drops them.
func TestOutboxStageFlushAndDiscard(t *testing.T) {
	var rec recorder
	cancel := Subscribe(rec.fn)
	t.Cleanup(cancel)

	ctx, outbox := WithOutbox(context.Background())
	Stage(ctx, Event{Name: "s1"})
	Stage(ctx, Event{Name: "s2"})
	Stage(ctx, Event{Name: "s3"})

	if len(rec.names) != 0 {
		t.Fatalf("staged events were published before Flush: %v", rec.names)
	}
	if got := outbox.Pending(); got != 3 {
		t.Fatalf("Pending() = %d, want 3", got)
	}

	outbox.Flush()
	if !equalStrings(rec.names, []string{"s1", "s2", "s3"}) {
		t.Fatalf("Flush published %v, want s1, s2, s3 in order", rec.names)
	}
	if got := outbox.Pending(); got != 0 {
		t.Fatalf("Pending() after Flush = %d, want 0", got)
	}

	outbox.Flush()
	if len(rec.names) != 3 {
		t.Fatalf("second Flush published again: %v", rec.names)
	}

	ctxDiscard, discarded := WithOutbox(context.Background())
	Stage(ctxDiscard, Event{Name: "d1"})
	discarded.Discard()
	discarded.Flush()
	if len(rec.names) != 3 {
		t.Fatalf("Flush after Discard published events: %v", rec.names)
	}
	if got := discarded.Pending(); got != 0 {
		t.Fatalf("Pending() after Discard = %d, want 0", got)
	}
}

// Wanted result 6: without an outbox (including a nil context) Stage never
// publishes, leaves other outboxes alone and logs the staged-outside message.
func TestStageWithoutOutboxDropsAndLogs(t *testing.T) {
	logs := captureLog(t)

	var rec recorder
	cancel := Subscribe(rec.fn)
	t.Cleanup(cancel)

	ctx, outbox := WithOutbox(context.Background())
	Stage(ctx, Event{Name: "kept-for-outbox"})
	pendingBefore := outbox.Pending()

	Stage(context.Background(), Event{Name: "no-outbox"})
	if len(rec.names) != 0 {
		t.Fatalf("Stage without outbox published immediately: %v", rec.names)
	}
	if got := outbox.Pending(); got != pendingBefore {
		t.Fatalf("Stage without outbox changed an unrelated outbox: Pending() = %d, want %d", got, pendingBefore)
	}
	if !strings.Contains(logs.String(), "staged outside a transaction outbox") {
		t.Fatalf("log = %q, want the staged-outside message", logs.String())
	}

	var nilCtx context.Context
	Stage(nilCtx, Event{Name: "nil-ctx"})
	if len(rec.names) != 0 {
		t.Fatalf("Stage with a nil context published immediately: %v", rec.names)
	}
	if got := outbox.Pending(); got != pendingBefore {
		t.Fatalf("Stage with a nil context changed an unrelated outbox: Pending() = %d, want %d", got, pendingBefore)
	}

	outbox.Flush()
	if !equalStrings(rec.names, []string{"kept-for-outbox"}) {
		t.Fatalf("Flush after dropped stages published %v, want only kept-for-outbox", rec.names)
	}
}

// Wanted result 7: Subscribe, cancel and Publish run concurrently without a
// data race (go test -race).
func TestSubscribeCancelPublishConcurrently(t *testing.T) {
	var delivered atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cancel := Subscribe(func(Event) { delivered.Add(1) })
				Publish(Event{Name: "concurrent"})
				cancel()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			Publish(Event{Name: "publisher"})
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent Subscribe, cancel and Publish did not finish")
	}
}
