package server

// Site extension points (docs/tickets/29-plugin-read-api-and-extensions.md, PR B):
// theme.inject, content.render and comments.component. notify.channel lives in
// plugin_extensions_notify.go. The generation counter and the provider list
// cache here are shared by all four points.
//
// Every plugin call is a cross-process request with a one-second deadline, so
// nothing on the public hot path calls a plugin per request: results are cached
// in process memory and keyed by the plugin generation, which every plugin or
// extension change bumps. A 60-second safety TTL covers changes made by another
// process (multi-instance deployments share nothing here, by design).

import (
	"context"
	"html/template"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"gorm.io/gorm"
)

const (
	// pluginExtensionSafetyTTL bounds how long any cached extension result is
	// used without recomputation, whatever the generation says.
	pluginExtensionSafetyTTL = 60 * time.Second
	// pluginExtensionProviderTTL is the lifetime of the cached active-provider
	// list of one point (also invalidated by the generation).
	pluginExtensionProviderTTL = 5 * time.Second
	// pluginExtensionNegativeTTL is how long a failed call is remembered before
	// the plugin is tried again.
	pluginExtensionNegativeTTL = 30 * time.Second

	// pluginThemeInjectMax is the largest head or footer fragment one plugin
	// may contribute; a larger fragment is skipped and logged.
	pluginThemeInjectMax = 64 << 10
	// pluginContentRenderMax is the largest HTML sent to content.render;
	// larger content is used as is.
	pluginContentRenderMax = 1 << 20
	// pluginContentRenderBudget is the total time one render chain may take
	// across all providers (each call still has its own one-second deadline).
	pluginContentRenderBudget = 3 * time.Second
	// pluginContentRenderEntries and pluginContentRenderBytes bound the render
	// cache (LRU by last use).
	pluginContentRenderEntries = 256
	pluginContentRenderBytes   = 32 << 20
)

// pluginExtensionGen is the plugin generation. It is bumped by every change
// that can alter what a provider returns or which providers are active:
// plugin enable, disable, reload, install, uninstall, settings writes
// (including the plugin's own settings.set), extension bindings, runtime
// replacement and a call that found a provider unavailable.
var pluginExtensionGen atomic.Uint64

// bumpPluginExtensionGeneration invalidates every cached extension result.
func bumpPluginExtensionGeneration() uint64 {
	return pluginExtensionGen.Add(1)
}

// pluginExtensionGeneration returns the current generation.
func pluginExtensionGeneration() uint64 {
	return pluginExtensionGen.Load()
}

// extensionPointMultiple reports whether several providers of point are used
// at once (in position order). Single points use the first available one.
func extensionPointMultiple(point string) bool {
	switch point {
	case ext.PointMediaStorage, ext.PointThemeInject, ext.PointContentRender, ext.PointNotifyChannel:
		return true
	}
	return false
}

// pluginExtensionLog records one line in the plugin's own log (system_logs,
// source plugin) through the same pipeline as JellyCMS.log. level is error,
// warn or info.
func pluginExtensionLog(pluginID, level, message string) {
	forwardPluginLog(pluginID, level, message)
}

// extensionProviderCache remembers the active provider list of each point for
// pluginExtensionProviderTTL within one generation.
var extensionProviderCache struct {
	sync.Mutex
	entries map[string]extensionProviderEntry
}

type extensionProviderEntry struct {
	gen uint64
	at  time.Time
	ids []string
}

// extensionActiveProviders lists the plugin providers of a multiple point that
// are live right now (loaded, callback registered, enabled, approval hash
// current), in the order they run: with an empty extension_bindings list every
// live provider in id order; with a non-empty list only the listed ones that
// are live, in position order. builtin is never included. The result is cached
// for pluginExtensionProviderTTL per generation; a nil db yields nil.
func extensionActiveProviders(ctx context.Context, db *gorm.DB, point string) []string {
	if db == nil || point == "" {
		return nil
	}
	gen := pluginExtensionGeneration()
	now := time.Now()
	extensionProviderCache.Lock()
	if entry, ok := extensionProviderCache.entries[point]; ok && entry.gen == gen && now.Sub(entry.at) < pluginExtensionProviderTTL {
		extensionProviderCache.Unlock()
		return append([]string(nil), entry.ids...)
	}
	extensionProviderCache.Unlock()

	live := liveExtensionPluginIDs(ctx, db, point)
	var active []string
	if err := db.WithContext(ctx).Table("extension_bindings").Where("point = ?", point).
		Order("position ASC, provider_id ASC").Pluck("provider_id", &active).Error; err != nil {
		return nil
	}
	ids := make([]string, 0, len(live))
	if len(active) == 0 {
		ids = append(ids, live...)
	} else {
		isLive := make(map[string]bool, len(live))
		for _, id := range live {
			isLive[id] = true
		}
		for _, id := range active {
			if isLive[id] {
				ids = append(ids, id)
			}
		}
	}
	extensionProviderCache.Lock()
	if extensionProviderCache.entries == nil {
		extensionProviderCache.entries = make(map[string]extensionProviderEntry)
	}
	extensionProviderCache.entries[point] = extensionProviderEntry{gen: gen, at: now, ids: append([]string(nil), ids...)}
	extensionProviderCache.Unlock()
	return ids
}

// pluginPostSummary is the post object every site extension receives: the
// identity of the post without its content.
func pluginPostSummary(post core.Post) map[string]any {
	return map[string]any{
		"id":        post.ID,
		"type":      post.Type,
		"slug":      post.Slug,
		"title":     post.Title,
		"author_id": post.AuthorID,
	}
}

// pluginThemeInjection is the HTML the active theme.inject providers add to a
// public page, after the admin's own code injection.
type pluginThemeInjection struct {
	Head   template.HTML
	Footer template.HTML
}

// pluginThemeInjectionFor returns the concatenated head and footer fragments
// of the active theme.inject providers in run order (extensionActiveProviders).
//
// Wanted results:
//   - Each provider is called with input {} and returns {"head": string, "footer": string};
//     either key may be missing. The result of one provider is cached per
//     (plugin id, generation) and reused for pluginExtensionSafetyTTL, so the
//     plugin is called once per generation, not per request.
//   - Fragments are output raw, in provider order, head after head and footer
//     after footer. The caller (applyPublicSEO) appends them to the admin's
//     code injection and applies the commerce-page rule itself.
//   - A provider that fails, times out, returns something that is not an
//     object of strings, or whose head or footer exceeds pluginThemeInjectMax
//     contributes nothing (the whole provider on an invalid shape; only the
//     oversized slot on a size breach), is logged once with
//     pluginExtensionLog(id, "error", …) and is not retried for
//     pluginExtensionNegativeTTL.
//   - A nil db, a nil runtime or no active provider yields an empty value and
//     calls nothing.
func pluginThemeInjectionFor(ctx context.Context, db *gorm.DB) pluginThemeInjection {
	// TODO(B2): implement with a per-plugin cache keyed by (id, generation).
	return pluginThemeInjection{}
}

// pluginRenderContent passes the public HTML of post through the active
// content.render providers in run order and returns the final HTML. It runs
// after publicPostContent and before theme.HeadingIDs, for posts, pages and
// the home landing page; feeds and the API never call it.
//
// Wanted results:
//   - Each provider is called with {"post": pluginPostSummary(post), "html": string}
//     and returns {"html": string}; the output of one provider is the input of
//     the next. The whole chain shares a pluginContentRenderBudget deadline.
//   - The final HTML is cached by (post.ID, sha256(input html), generation) in
//     an LRU of at most pluginContentRenderEntries entries and
//     pluginContentRenderBytes total bytes; a hit calls no plugin (the runtime
//     ExtensionCalls counter does not move). Concurrent misses for the same key
//     compute once (single flight). Entries also expire after
//     pluginExtensionSafetyTTL.
//   - Any provider failure (error, timeout, missing or non-string html, output
//     over 1 MiB) returns the original html unchanged, logs
//     pluginExtensionLog(id, "error", …) once and caches the failure for
//     pluginExtensionNegativeTTL so the page keeps rendering without the plugin.
//   - html longer than pluginContentRenderMax, a nil db or runtime, or no
//     active provider returns html as is without calling anything.
func pluginRenderContent(ctx context.Context, db *gorm.DB, post core.Post, html string) string {
	// TODO(B2): implement with the LRU cache and single flight described above.
	return html
}

// pluginCommentsComponent returns the comments section a comments.component
// provider renders for post, wrapped by the core in
// <section class="comments" id="comments">…</section>, and true. It returns
// false when the builtin comments block should render: no provider is
// selected (extensionSelection yields builtin), the provider is unavailable,
// or the call failed.
//
// Wanted results:
//   - The single provider is the first available one of the active list
//     (extensionSelection for ext.PointCommentsComponent); it is called with
//     {"post": pluginPostSummary(post)} and returns {"html": string}.
//   - The result is cached by (post.ID, generation) for pluginExtensionSafetyTTL.
//   - A failure (error, timeout, missing or non-string html) is logged with
//     pluginExtensionLog(id, "error", …), cached for pluginExtensionNegativeTTL
//     and yields false so the theme's own block renders.
//   - The caller skips loading the comment tree when ok is true; the post page
//     shows the "留言" button when CommentsHTML is set.
func pluginCommentsComponent(ctx context.Context, db *gorm.DB, post core.Post) (template.HTML, bool) {
	// TODO(B2): implement with a per-post cache keyed by (post.ID, generation).
	return "", false
}
