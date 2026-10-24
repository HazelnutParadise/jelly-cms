// Package ext is the registry of extension points. A point is a named slot
// that a feature resolves providers for; the admin extensions page edits the
// active list per point (extension_bindings).
//
// Shipped points have a working implementation in the core: search.engine and
// media.storage. Declared points are reserved names only: plugins may declare
// them in the manifest, but nothing calls them yet.
package ext

import (
	"errors"
	"fmt"
	"sync"
)

// Shipped extension points.
const (
	PointSearchEngine = "search.engine"
	PointMediaStorage = "media.storage"
)

// Declared extension points. media.processor is reserved for WebP conversion,
// which ticket 08 defers.
const (
	PointMediaProcessor = "media.processor"
)

// ErrUnknownPoint is returned when a point name is neither shipped nor declared.
var ErrUnknownPoint = errors.New("ext: unknown extension point")

// Provider is one registered implementation of a point.
type Provider struct {
	ID       string
	Provider any
}

var (
	mu        sync.RWMutex
	shipped   = map[string]bool{PointSearchEngine: true, PointMediaStorage: true}
	declared  = map[string]bool{PointMediaProcessor: true}
	providers = map[string][]Provider{}
)

// Known reports whether point is shipped or declared.
func Known(point string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return shipped[point] || declared[point]
}

// Shipped reports whether point has a working implementation in the core.
func Shipped(point string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return shipped[point]
}

// Register adds provider id to point. A provider with the same id replaces the
// earlier one, so a reloaded plugin does not duplicate itself.
func Register(point, id string, provider any) error {
	if id == "" {
		return errors.New("ext: provider id is required")
	}
	mu.Lock()
	defer mu.Unlock()
	if !shipped[point] && !declared[point] {
		return fmt.Errorf("%w: %s", ErrUnknownPoint, point)
	}
	list := providers[point]
	for i := range list {
		if list[i].ID == id {
			list[i].Provider = provider
			providers[point] = list
			return nil
		}
	}
	providers[point] = append(list, Provider{ID: id, Provider: provider})
	return nil
}

// Resolve returns the providers registered for point in registration order.
func Resolve(point string) []Provider {
	mu.RLock()
	defer mu.RUnlock()
	return append([]Provider(nil), providers[point]...)
}
