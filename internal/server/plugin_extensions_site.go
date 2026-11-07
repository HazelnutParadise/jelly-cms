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
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
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
	if currentExtensionRuntime() == nil || db == nil {
		return pluginThemeInjection{}
	}
	var head, footer strings.Builder
	for _, id := range extensionActiveProviders(ctx, db, ext.PointThemeInject) {
		h, f, ok := pluginThemeInjectFor(ctx, db, id)
		if !ok {
			continue
		}
		head.WriteString(h)
		footer.WriteString(f)
	}
	return pluginThemeInjection{Head: template.HTML(head.String()), Footer: template.HTML(footer.String())}
}

// pluginThemeInjectKey identifies one provider's theme.inject result.
type pluginThemeInjectKey struct {
	id  string
	gen uint64
}

// pluginThemeInjectEntry is one cached theme.inject result. failed entries are
// remembered for pluginExtensionNegativeTTL, the others for the safety TTL.
type pluginThemeInjectEntry struct {
	at     time.Time
	head   string
	footer string
	failed bool
}

var pluginThemeInjectCache struct {
	sync.Mutex
	entries map[pluginThemeInjectKey]pluginThemeInjectEntry
}

// pluginCacheBound is the map size above which a cache map is rebuilt on write.
const pluginCacheBound = 1024

// pluginThemeInjectFor returns the head and footer fragments of one provider
// for the current generation, calling it at most once per generation (and
// once per negative TTL after a failure). ok is false when the provider
// contributes nothing.
func pluginThemeInjectFor(ctx context.Context, db *gorm.DB, id string) (head, footer string, ok bool) {
	gen := pluginExtensionGeneration()
	key := pluginThemeInjectKey{id: id, gen: gen}
	now := time.Now()
	pluginThemeInjectCache.Lock()
	entry, hit := pluginThemeInjectCache.entries[key]
	pluginThemeInjectCache.Unlock()
	if hit {
		ttl := pluginExtensionSafetyTTL
		if entry.failed {
			ttl = pluginExtensionNegativeTTL
		}
		if now.Sub(entry.at) < ttl {
			return entry.head, entry.footer, !entry.failed
		}
	}

	// A provider that is not available is skipped without caching: the
	// provider list cache already decides which providers are live.
	runtime, meta, err := extensionPluginMeta(ctx, db, id, ext.PointThemeInject)
	if err != nil {
		return "", "", false
	}
	fail := func(reason string) (string, string, bool) {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: %s", ext.PointThemeInject, reason))
		storePluginThemeInject(key, pluginThemeInjectEntry{at: time.Now(), failed: true})
		return "", "", false
	}
	output, err := runtime.CallExtension(ctx, meta.ID, ext.PointThemeInject, []byte("{}"))
	if err != nil {
		if ctx.Err() != nil {
			// The request went away; that says nothing about the plugin.
			return "", "", false
		}
		return fail(fmt.Sprintf("call failed: %v", err))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output, &fields); err != nil || fields == nil {
		return fail("result is not an object")
	}
	headText, headOK := pluginOptionalString(fields, "head")
	footerText, footerOK := pluginOptionalString(fields, "footer")
	if !headOK || !footerOK {
		return fail("head and footer must be strings when present")
	}
	if len(headText) > pluginThemeInjectMax {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: head is %d bytes, above the %d byte limit; head skipped", ext.PointThemeInject, len(headText), pluginThemeInjectMax))
		headText = ""
	}
	if len(footerText) > pluginThemeInjectMax {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: footer is %d bytes, above the %d byte limit; footer skipped", ext.PointThemeInject, len(footerText), pluginThemeInjectMax))
		footerText = ""
	}
	storePluginThemeInject(key, pluginThemeInjectEntry{at: time.Now(), head: headText, footer: footerText})
	return headText, footerText, true
}

// storePluginThemeInject writes one entry, rebuilding the map when it grows past pluginCacheBound.
func storePluginThemeInject(key pluginThemeInjectKey, entry pluginThemeInjectEntry) {
	pluginThemeInjectCache.Lock()
	defer pluginThemeInjectCache.Unlock()
	if pluginThemeInjectCache.entries == nil || len(pluginThemeInjectCache.entries) > pluginCacheBound {
		pluginThemeInjectCache.entries = make(map[pluginThemeInjectKey]pluginThemeInjectEntry)
	}
	pluginThemeInjectCache.entries[key] = entry
}

// pluginOptionalString reads fields[name] as a string. A missing key yields
// ("", true); a present value that is not a JSON string yields ok false.
func pluginOptionalString(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, present := fields[name]
	if !present {
		return "", true
	}
	return pluginJSONString(raw)
}

// pluginJSONString decodes raw only when it is a JSON string.
func pluginJSONString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return "", false
	}
	return s, true
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
	if currentExtensionRuntime() == nil || db == nil || len(html) > pluginContentRenderMax {
		return html
	}
	ids := extensionActiveProviders(ctx, db, ext.PointContentRender)
	if len(ids) == 0 {
		return html
	}
	key := pluginRenderKey{postID: post.ID, sum: sha256.Sum256([]byte(html)), gen: pluginExtensionGeneration()}

	cache := &pluginRenderCache
	cache.Lock()
	if cache.index == nil {
		cache.order = list.New()
		cache.index = make(map[pluginRenderKey]*list.Element)
		cache.flights = make(map[pluginRenderKey]*pluginRenderFlight)
	}
	if entry, ok := cache.lookupLocked(key, time.Now()); ok {
		cache.Unlock()
		if entry.failed {
			return html
		}
		return entry.html
	}
	if flight, ok := cache.flights[key]; ok {
		cache.Unlock()
		select {
		case <-flight.done:
			return flight.html
		case <-ctx.Done():
			return html
		}
	}
	flight := &pluginRenderFlight{done: make(chan struct{})}
	cache.flights[key] = flight
	cache.Unlock()

	result := html
	var entry *pluginRenderEntry
	defer func() {
		cache.Lock()
		delete(cache.flights, key)
		if entry != nil {
			cache.storeLocked(entry)
		}
		cache.Unlock()
		flight.html = result
		close(flight.done)
	}()

	out, failedID, err := pluginRenderChain(ctx, db, post, html, ids)
	switch {
	case err == nil:
		result = out
		entry = &pluginRenderEntry{key: key, at: time.Now(), html: out}
	case ctx.Err() != nil:
		// The request went away; that says nothing about the plugins, so the
		// failure is neither logged nor remembered.
	default:
		pluginExtensionLog(failedID, "error", fmt.Sprintf("%s: %v; original content kept", ext.PointContentRender, err))
		entry = &pluginRenderEntry{key: key, at: time.Now(), failed: true}
	}
	return result
}

// pluginRenderKey identifies one render result: the post, the hash of the
// input HTML and the plugin generation.
type pluginRenderKey struct {
	postID uint
	sum    [32]byte
	gen    uint64
}

// pluginRenderEntry is one cached render result (html is empty for failures).
type pluginRenderEntry struct {
	key    pluginRenderKey
	at     time.Time
	html   string
	failed bool
}

// pluginRenderFlight lets concurrent misses for one key wait for the leader.
type pluginRenderFlight struct {
	done chan struct{}
	html string
}

// pluginRenderCache is the LRU of content.render results with single flight.
// order holds *pluginRenderEntry, most recently used first.
var pluginRenderCache pluginRenderCacheState

type pluginRenderCacheState struct {
	sync.Mutex
	order   *list.List
	index   map[pluginRenderKey]*list.Element
	bytes   int
	flights map[pluginRenderKey]*pluginRenderFlight
}

// lookupLocked returns a live entry and refreshes its recency. It must hold the lock.
func (c *pluginRenderCacheState) lookupLocked(key pluginRenderKey, now time.Time) (*pluginRenderEntry, bool) {
	el, ok := c.index[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*pluginRenderEntry)
	ttl := pluginExtensionSafetyTTL
	if entry.failed {
		ttl = pluginExtensionNegativeTTL
	}
	if now.Sub(entry.at) >= ttl {
		c.removeLocked(el)
		return nil, false
	}
	c.order.MoveToFront(el)
	return entry, true
}

// storeLocked inserts entry and evicts least recently used entries until both
// bounds hold. It must hold the lock.
func (c *pluginRenderCacheState) storeLocked(entry *pluginRenderEntry) {
	if el, ok := c.index[entry.key]; ok {
		c.removeLocked(el)
	}
	c.index[entry.key] = c.order.PushFront(entry)
	c.bytes += len(entry.html)
	for c.order.Len() > pluginContentRenderEntries || c.bytes > pluginContentRenderBytes {
		back := c.order.Back()
		if back == nil {
			break
		}
		c.removeLocked(back)
	}
}

// removeLocked drops one element. It must hold the lock.
func (c *pluginRenderCacheState) removeLocked(el *list.Element) {
	entry := el.Value.(*pluginRenderEntry)
	c.order.Remove(el)
	delete(c.index, entry.key)
	c.bytes -= len(entry.html)
}

// pluginRenderChain runs html through every provider in order. On failure it
// returns the id of the provider that failed and the reason.
func pluginRenderChain(ctx context.Context, db *gorm.DB, post core.Post, html string, ids []string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, pluginContentRenderBudget)
	defer cancel()
	summary := pluginPostSummary(post)
	current := html
	for _, id := range ids {
		runtime, meta, err := extensionPluginMeta(ctx, db, id, ext.PointContentRender)
		if err != nil {
			return "", id, fmt.Errorf("provider unavailable: %w", err)
		}
		input, err := json.Marshal(map[string]any{"post": summary, "html": current})
		if err != nil {
			return "", id, fmt.Errorf("encode input: %w", err)
		}
		output, err := runtime.CallExtension(ctx, meta.ID, ext.PointContentRender, input)
		if err != nil {
			return "", id, fmt.Errorf("call failed: %w", err)
		}
		next, err := pluginHTMLOutput(output, pluginContentRenderMax)
		if err != nil {
			return "", id, err
		}
		current = next
	}
	return current, "", nil
}

// pluginHTMLOutput reads the required html string of a provider result and
// enforces limit on its size.
func pluginHTMLOutput(output json.RawMessage, limit int) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output, &fields); err != nil || fields == nil {
		return "", errors.New("result is not an object")
	}
	raw, present := fields["html"]
	if !present {
		return "", errors.New("result has no html")
	}
	s, ok := pluginJSONString(raw)
	if !ok {
		return "", errors.New("html is not a string")
	}
	if len(s) > limit {
		return "", fmt.Errorf("html is %d bytes, above the %d byte limit", len(s), limit)
	}
	return s, nil
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
	if currentExtensionRuntime() == nil || db == nil {
		return "", false
	}
	id, isJS := extensionSelection(ctx, db, ext.PointCommentsComponent)
	if !isJS || id == extensionBuiltinID {
		return "", false
	}
	key := pluginCommentsKey{postID: post.ID, gen: pluginExtensionGeneration()}
	now := time.Now()
	pluginCommentsCache.Lock()
	entry, hit := pluginCommentsCache.entries[key]
	pluginCommentsCache.Unlock()
	if hit {
		ttl := pluginExtensionSafetyTTL
		if entry.failed {
			ttl = pluginExtensionNegativeTTL
		}
		if now.Sub(entry.at) < ttl {
			return entry.html, !entry.failed
		}
	}

	runtime, meta, err := extensionPluginMeta(ctx, db, id, ext.PointCommentsComponent)
	if err != nil {
		return "", false
	}
	fail := func(reason string) (template.HTML, bool) {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: %s; builtin comments kept", ext.PointCommentsComponent, reason))
		storePluginComments(key, pluginCommentsEntry{at: time.Now(), failed: true})
		return "", false
	}
	input, err := json.Marshal(map[string]any{"post": pluginPostSummary(post)})
	if err != nil {
		return fail(fmt.Sprintf("encode input failed: %v", err))
	}
	output, err := runtime.CallExtension(ctx, meta.ID, ext.PointCommentsComponent, input)
	if err != nil {
		if ctx.Err() != nil {
			// The request went away; that says nothing about the plugin.
			return "", false
		}
		return fail(fmt.Sprintf("call failed: %v", err))
	}
	fragment, err := pluginHTMLOutput(output, pluginContentRenderMax)
	if err != nil {
		return fail(err.Error())
	}
	html := template.HTML(`<section class="comments" id="comments">` + fragment + `</section>`)
	storePluginComments(key, pluginCommentsEntry{at: time.Now(), html: html})
	return html, true
}

// pluginCommentsKey identifies one post's comments.component result.
type pluginCommentsKey struct {
	postID uint
	gen    uint64
}

// pluginCommentsEntry is one cached comments.component result.
type pluginCommentsEntry struct {
	at     time.Time
	html   template.HTML
	failed bool
}

var pluginCommentsCache struct {
	sync.Mutex
	entries map[pluginCommentsKey]pluginCommentsEntry
}

// storePluginComments writes one entry, rebuilding the map when it grows past pluginCacheBound.
func storePluginComments(key pluginCommentsKey, entry pluginCommentsEntry) {
	pluginCommentsCache.Lock()
	defer pluginCommentsCache.Unlock()
	if pluginCommentsCache.entries == nil || len(pluginCommentsCache.entries) > pluginCacheBound {
		pluginCommentsCache.entries = make(map[pluginCommentsKey]pluginCommentsEntry)
	}
	pluginCommentsCache.entries[key] = entry
}
