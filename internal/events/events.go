// Package events is the in-process event bus that hands site events (post
// saved, order paid, user logged in, ...) to subscribers such as the plugin
// runtime bridge. Events are published after the transaction that produced
// them commits: handlers stage events on the context outbox and the outbox is
// flushed only when the transaction returns nil. See docs/tickets/27-plugin-events.md.
package events

import (
	"context"
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

// Subscribe registers fn and returns a function that removes it again. Wanted
// results:
//   - events published after Subscribe reach fn in order; after cancel they do not
//   - several subscribers each receive every event; a panicking subscriber is
//     recovered, logged, and never stops the others
//   - Subscribe and cancel are safe to call concurrently with Publish
func Subscribe(fn Subscriber) (cancel func()) {
	return func() {}
}

// Publish delivers e to every subscriber now, in subscription order. A nil
// Payload becomes an empty map and a zero At becomes time.Now().UTC().
func Publish(e Event) {}

// Outbox holds events staged inside one transaction until Flush or Discard.
type Outbox struct{}

// WithOutbox returns ctx carrying a new outbox. When ctx already carries one
// the same outbox and ctx are returned, so nested transactions share the
// outermost outbox and flush exactly once.
func WithOutbox(ctx context.Context) (context.Context, *Outbox) {
	return ctx, nil
}

// OutboxFrom returns the outbox carried by ctx, or nil.
func OutboxFrom(ctx context.Context) *Outbox { return nil }

// Stage appends e to the outbox carried by ctx. Without an outbox the event
// is DROPPED and "events: <name> staged outside a transaction outbox" is
// written with log.Printf: gorm's tx.Statement.Context is never nil, so an
// immediate publish here would fire before commit or after rollback. Code
// without a transaction publishes explicitly with Publish. A nil ctx behaves
// like a ctx without outbox.
func Stage(ctx context.Context, e Event) {}

// Flush publishes the staged events in order and empties the outbox. A second
// Flush, or a Flush after Discard, publishes nothing.
func (o *Outbox) Flush() {}

// Discard drops the staged events; a later Flush publishes nothing.
func (o *Outbox) Discard() {}

// Pending returns the number of staged events (for tests and diagnostics).
func (o *Outbox) Pending() int { return 0 }

// Transaction runs fn inside db.WithContext(ctx).Transaction with an outbox on
// the context. Events staged through tx.Statement.Context are published when
// fn returns nil and the commit succeeds; on any error they are discarded.
// When ctx already carries an outbox (a nested call) the events stay staged
// on that outer outbox and Transaction neither flushes nor discards them.
func Transaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(fn)
}
