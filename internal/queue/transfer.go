package queue

import (
	"context"
	"time"
)

// TransferSource is an optional extension for durable queues. SnapshotQueued
// must not mutate the source; ConfirmQueued removes only IDs still queued.
// Keeping this separate preserves the public Queue contract for plugins.
type TransferSource interface {
	SnapshotQueued(context.Context, int) ([]Job, error)
	ConfirmQueued(context.Context, []string) error
}

// LeaseExtender lets long-running handlers renew a lease without changing the
// stable Queue interface. Renew before the current lease expires.
type LeaseExtender interface {
	ExtendLease(context.Context, string, string, time.Duration) error
}
