// Package ext is the registry of extension points. A point is a named slot
// that a feature resolves providers for; the admin extensions page edits the
// active list per point (extension_bindings).
//
// Shipped points have a working call site in the core: search.engine,
// media.storage, theme.inject, content.render, comments.component and
// notify.channel. A shipped point may also have a builtin provider (the core's
// own implementation, admin id "builtin"); points without one simply do
// nothing when no plugin provides them. Declared points are reserved names
// only: plugins may declare them in the manifest, but nothing calls them yet.
package ext

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Shipped extension points.
const (
	PointSearchEngine      = "search.engine"
	PointMediaStorage      = "media.storage"
	PointThemeInject       = "theme.inject"
	PointContentRender     = "content.render"
	PointCommentsComponent = "comments.component"
	PointNotifyChannel     = "notify.channel"
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
	mu      sync.RWMutex
	shipped = map[string]bool{
		PointSearchEngine:      true,
		PointMediaStorage:      true,
		PointThemeInject:       true,
		PointContentRender:     true,
		PointCommentsComponent: true,
		PointNotifyChannel:     true,
	}
	// builtin lists the shipped points the core implements itself when no
	// plugin is selected: the PostgreSQL search, local media storage and the
	// theme's own comments block.
	builtin = map[string]bool{
		PointSearchEngine:      true,
		PointMediaStorage:      true,
		PointCommentsComponent: true,
	}
	declared  = map[string]bool{PointMediaProcessor: true}
	providers = map[string][]Provider{}
)

// Points lists every shipped and declared point in name order.
func Points() []string {
	mu.RLock()
	defer mu.RUnlock()

	points := make([]string, 0, len(shipped)+len(declared))
	for point := range shipped {
		points = append(points, point)
	}
	for point := range declared {
		if !shipped[point] {
			points = append(points, point)
		}
	}
	sort.Strings(points)

	return points
}

// ShippedPoints lists the shipped points in name order.
func ShippedPoints() []string {
	mu.RLock()
	defer mu.RUnlock()

	points := make([]string, 0, len(shipped))
	for point := range shipped {
		points = append(points, point)
	}
	sort.Strings(points)

	return points
}

// Known reports whether point is shipped or declared.
func Known(point string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return shipped[point] || declared[point]
}

// Shipped reports whether point has a working call site in the core.
func Shipped(point string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return shipped[point]
}

// HasBuiltin reports whether the core itself provides point when no plugin
// is selected. A shipped point without a builtin is simply inactive then.
func HasBuiltin(point string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return builtin[point]
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
