// Package events is the in-process event bus that hands site events (post
// saved, order paid, user logged in, ...) to subscribers such as the plugin
// runtime bridge. Events are published after the transaction that produced
// them commits: handlers stage events on the context outbox and the outbox is
// flushed only when the transaction returns nil. See ticket 28.
package events

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Event is one site event. Name is the hook name the plugin runtime
// dispatches, for example "OnPostSave". Payload must be JSON encodable.
type Event struct {
	Name    string
	At      time.Time
	Payload map[string]any
}

// Subscriber receives every published event in publish order. Publish runs it
// on the publishing goroutine (often an HTTP handler after commit), so it must
// return quickly and never block; long work belongs in a queue the subscriber
// owns, like the plugin runtime's per-plugin event queues.
type Subscriber func(Event)

// subscription pairs a subscriber with the id that cancel uses to remove it.
type subscription struct {
	id uint64
	fn Subscriber
}

var (
	// subMu guards subs and nextSubID. Writers always install a freshly built
	// slice, so a Publish snapshot taken under RLock is never modified later.
	subMu     sync.RWMutex
	subs      []subscription
	nextSubID uint64
)

// Subscribe registers fn and returns a function that removes it again. Wanted
// results:
//   - events published after Subscribe reach fn in order; after cancel they do not
//   - several subscribers each receive every event; a panicking subscriber is
//     recovered, logged, and never stops the others
//   - Subscribe and cancel are safe to call concurrently with Publish
func Subscribe(fn Subscriber) (cancel func()) {
	if fn == nil {
		return func() {}
	}

	subMu.Lock()
	nextSubID++
	id := nextSubID
	next := make([]subscription, 0, len(subs)+1)
	next = append(next, subs...)
	next = append(next, subscription{id: id, fn: fn})
	subs = next
	subMu.Unlock()

	return func() {
		subMu.Lock()
		defer subMu.Unlock()
		next := make([]subscription, 0, len(subs))
		for _, s := range subs {
			if s.id != id {
				next = append(next, s)
			}
		}
		subs = next
	}
}

// Publish delivers e to every subscriber now, in subscription order. A nil
// Payload becomes an empty map and a zero At becomes time.Now().UTC().
func Publish(e Event) {
	e = normalize(e)

	subMu.RLock()
	snapshot := subs
	subMu.RUnlock()

	// Subscribers run outside the lock so they may Subscribe or cancel
	// without deadlocking.
	for _, s := range snapshot {
		deliver(s.fn, e)
	}
}

// normalize fills the defaults Publish promises for nil payloads and zero times.
func normalize(e Event) Event {
	if e.Payload == nil {
		e.Payload = map[string]any{}
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return e
}

// deliver calls one subscriber and recovers a panic so the rest still run.
func deliver(fn Subscriber, e Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("events: subscriber panic for %s: %v", e.Name, r)
		}
	}()
	fn(e)
}

// outboxKey is the unexported context key under which an Outbox is stored.
type outboxKey struct{}

// Outbox holds events staged inside one transaction until Flush or Discard.
type Outbox struct {
	mu      sync.Mutex
	pending []Event
	// closed is set by Flush or Discard; afterwards Stage drops events and
	// Flush publishes nothing.
	closed bool
}

// WithOutbox returns ctx carrying a new outbox. When ctx already carries one
// the same outbox and ctx are returned, so nested transactions share the
// outermost outbox and flush exactly once.
func WithOutbox(ctx context.Context) (context.Context, *Outbox) {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing := OutboxFrom(ctx); existing != nil {
		return ctx, existing
	}
	o := &Outbox{}
	return context.WithValue(ctx, outboxKey{}, o), o
}

// OutboxFrom returns the outbox carried by ctx, or nil.
func OutboxFrom(ctx context.Context) *Outbox {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(outboxKey{}).(*Outbox)
	return o
}

// Stage appends e to the outbox carried by ctx. Without an outbox the event
// is DROPPED and "events: <name> staged outside a transaction outbox" is
// written with log.Printf: gorm's tx.Statement.Context is never nil, so an
// immediate publish here would fire before commit or after rollback. Code
// without a transaction publishes explicitly with Publish. A nil ctx behaves
// like a ctx without outbox.
func Stage(ctx context.Context, e Event) {
	o := OutboxFrom(ctx)
	if o == nil {
		log.Printf("events: %s staged outside a transaction outbox", e.Name)
		return
	}
	o.stage(e)
}

// stage queues e unless the outbox was already flushed or discarded.
func (o *Outbox) stage(e Event) {
	e = normalize(e)

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.pending = append(o.pending, e)
}

// Flush publishes the staged events in order and empties the outbox. A second
// Flush, or a Flush after Discard, publishes nothing.
func (o *Outbox) Flush() {
	if o == nil {
		return
	}

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	batch := o.pending
	o.pending = nil
	o.closed = true
	o.mu.Unlock()

	// Publish outside the lock so a subscriber cannot deadlock on the outbox.
	for _, e := range batch {
		Publish(e)
	}
}

// Discard drops the staged events; a later Flush publishes nothing.
func (o *Outbox) Discard() {
	if o == nil {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending = nil
	o.closed = true
}

// Pending returns the number of staged events (for tests and diagnostics).
func (o *Outbox) Pending() int {
	if o == nil {
		return 0
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending)
}

// Transaction runs fn inside db.WithContext(ctx).Transaction with an outbox on
// the context. Events staged through tx.Statement.Context are published when
// fn returns nil and the commit succeeds; on any error they are discarded.
// When ctx already carries an outbox (a nested call) the events stay staged
// on that outer outbox and Transaction neither flushes nor discards them.
func Transaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	if db == nil {
		return errors.New("events: nil database")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if OutboxFrom(ctx) != nil {
		return db.WithContext(ctx).Transaction(fn)
	}

	ctx, outbox := WithOutbox(ctx)
	// Discard runs on every exit path that did not Flush, including a panic
	// in fn. After a successful Flush it is a no-op.
	defer outbox.Discard()

	if err := db.WithContext(ctx).Transaction(fn); err != nil {
		return err
	}
	outbox.Flush()
	return nil
}
