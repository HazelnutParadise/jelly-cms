//go:build !nats

package queue

import "fmt"

func openNATSQueue(Config) (Queue, error) {
	return nil, fmt.Errorf("queue: NATS JetStream driver requires build tag nats: %w", ErrNotImplemented)
}
