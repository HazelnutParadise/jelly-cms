package server

// Site extension points (ticket 29, PR B):
// theme.inject, content.render and comments.component. notify.channel lives in
// plugin_extensions_notify.go. The generation counter and the provider list
// cache here are shared by all four points.
//
// Nothing on the public hot path calls a plugin per request: results are cached
// in process memory and keyed by the plugin generation, which every plugin or
// extension change bumps. A 60-second safety TTL covers changes made by another
// process (multi-instance deployments share nothing here, by design). Plugin
// calls run on a context detached from the request with the cell's own budget
// (pluginCallContext): a client that goes away must not terminate the worker.

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

const (
	// pluginExtensionSafetyTTL bounds how long any cached extension result is
	// used without recomputation, whatever the generation says.
	pluginExtensionSafetyTTL = 60 * time.Second
	// pluginExtensionProviderTTL is the lifetime of the cached active-provider
	// list and selected provider of one point (also invalidated by the generation).
	pluginExtensionProviderTTL = 5 * time.Second
	// pluginExtensionNegativeTTL is how long a failed call is remembered before
	// the plugin is tried again.
	pluginExtensionNegativeTTL = 30 * time.Second

	// pluginCallMargin is kept back from the caller's deadline so the response
	// can still be written after a plugin call.
	pluginCallMargin = 300 * time.Millisecond

	// pluginThemeInjectMax is the largest head or footer fragment one plugin
	// may contribute; a larger fragment is skipped and logged.
	pluginThemeInjectMax = 64 << 10
	// pluginThemeInjectBudget is the total time the whole theme.inject provider
	// group may take per request miss.
	pluginThemeInjectBudget = 3 * time.Second
	// pluginContentRenderMax is the largest encoded input (and output html)
	// content.render handles; larger content is used as is.
	pluginContentRenderMax = 1 << 20
	// pluginContentRenderBudget is the total time one render chain may take
	// across all providers.
	pluginContentRenderBudget = 3 * time.Second
	// pluginCommentsBudget is the time one comments.component call may take.
	pluginCommentsBudget = time.Second
	// pluginContentRenderEntries and pluginContentRenderBytes bound the render
	// cache (LRU by last use).
	pluginContentRenderEntries = 256
	pluginContentRenderBytes   = 32 << 20
)

// pluginExtensionGen is the plugin generation. It is bumped by every change
// that can alter what a provider returns or which providers are active:
// plugin enable, disable, reload, install, uninstall, settings writes
// (including the plugin's own settings.set), extension bindings, runtime
// replacement, and the first sighting in a generation of a loaded plugin whose
// extension callback is gone (see noteLoadedProviderUnavailable).
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

// pluginCallBudget returns how long a plugin call started now may run: budget,
// cut to what is left of the caller's deadline minus pluginCallMargin. It is 0
// when the caller is already done or too close to its deadline for a plugin
// call; callers then skip the call without logging or caching a failure.
func pluginCallBudget(ctx context.Context, budget time.Duration) time.Duration {
	if ctx.Err() != nil {
		return 0
	}
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline) - pluginCallMargin; left < budget {
			budget = left
		}
	}
	if budget < 0 {
		return 0
	}
	return budget
}

// pluginCallContext returns the context for one plugin call, or for a group of
// calls that share one budget. It is detached from the request, so a request
// that is cancelled or times out does not terminate the plugin worker
// (workerProcess.request). Its own deadline is budget, cut to the caller's
// remaining time (pluginCallBudget); with no budget left it is already expired.
func pluginCallContext(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), pluginCallBudget(ctx, budget))
}

// pluginEncodeInput encodes a plugin input without escaping HTML, so the size
// checked against pluginContentRenderMax is the size actually sent. The
// trailing newline of the encoder is dropped.
func pluginEncodeInput(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// pluginEntryFresh reports whether a cached result may still be used: failures
// for pluginExtensionNegativeTTL, successes for pluginExtensionSafetyTTL.
func pluginEntryFresh(at time.Time, failed bool) bool {
	ttl := pluginExtensionSafetyTTL
	if failed {
		ttl = pluginExtensionNegativeTTL
	}
	return time.Since(at) < ttl
}

// prunePluginCache keeps a cache map within pluginCacheBound. Over the bound,
// stale entries are dropped first; the map is rebuilt only if it is still over.
func prunePluginCache[K comparable, V any](entries map[K]V, stale func(K, V) bool) map[K]V {
	if len(entries) <= pluginCacheBound {
		return entries
	}
	for k, v := range entries {
		if stale(k, v) {
			delete(entries, k)
		}
	}
	if len(entries) > pluginCacheBound {
		return make(map[K]V)
	}
	return entries
}

// pluginProviderUnavailable is the error extensionPluginMeta returns for a provider
// that is not live right now.
const pluginProviderUnavailable = "extension provider is unavailable"

// pluginProviderMeta is extensionPluginMeta for a call made on callCtx. An
// unavailable provider is reported to the caller, which skips it. When the
// plugin is still loaded but its callback is gone (typically its worker died),
// the first sighting per generation is noted: the generation is bumped so the
// provider list is recomputed at once.
func pluginProviderMeta(callCtx context.Context, db *gorm.DB, id, point string) (*plugin.Runtime, plugin.PluginMetadata, error) {
	runtime, meta, err := extensionPluginMeta(callCtx, db, id, point)
	if err != nil && err.Error() == pluginProviderUnavailable && callCtx.Err() == nil {
		if rt := currentExtensionRuntime(); rt != nil && rt.IsLoaded(id) {
			noteLoadedProviderUnavailable(id, point)
		}
	}
	return runtime, meta, err
}

// pluginUnavailableKey identifies one unavailable-but-loaded note.
type pluginUnavailableKey struct {
	id  string
	gen uint64
}

var pluginUnavailableNotes struct {
	sync.Mutex
	seen map[pluginUnavailableKey]bool
}

// noteLoadedProviderUnavailable bumps the generation and logs once per plugin
// and generation, so a dead worker does not flood the log. The bump makes the
// next provider list drop the plugin.
func noteLoadedProviderUnavailable(id, point string) {
	key := pluginUnavailableKey{id: id, gen: pluginExtensionGeneration()}
	notes := &pluginUnavailableNotes
	notes.Lock()
	if notes.seen == nil || len(notes.seen) > pluginCacheBound {
		notes.seen = make(map[pluginUnavailableKey]bool)
	}
	if notes.seen[key] {
		notes.Unlock()
		return
	}
	notes.seen[key] = true
	notes.Unlock()
	bumpPluginExtensionGeneration()
	pluginExtensionLog(id, "error", fmt.Sprintf("%s: extension provider is unavailable while the plugin is loaded (its worker may have stopped); provider list recomputed", point))
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

// extensionSelectionCache remembers the selected provider of each single point
// (extensionSelection) for pluginExtensionProviderTTL within one generation.
var extensionSelectionCache struct {
	sync.Mutex
	entries map[string]extensionSelectionEntry
}

type extensionSelectionEntry struct {
	gen  uint64
	at   time.Time
	id   string
	isJS bool
}

// extensionSelectedProvider is extensionSelection with a short cache per
// generation. The lookup runs on a context detached from the request, so a
// client that goes away cannot leave a builtin selection cached by mistake.
func extensionSelectedProvider(ctx context.Context, db *gorm.DB, point string) (string, bool) {
	gen := pluginExtensionGeneration()
	now := time.Now()
	cache := &extensionSelectionCache
	cache.Lock()
	if entry, ok := cache.entries[point]; ok && entry.gen == gen && now.Sub(entry.at) < pluginExtensionProviderTTL {
		cache.Unlock()
		return entry.id, entry.isJS
	}
	cache.Unlock()

	id, isJS := extensionSelection(context.WithoutCancel(ctx), db, point)
	cache.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]extensionSelectionEntry)
	}
	cache.entries = prunePluginCache(cache.entries, func(_ string, e extensionSelectionEntry) bool { return e.gen != gen })
	cache.entries[point] = extensionSelectionEntry{gen: gen, at: now, id: id, isJS: isJS}
	cache.Unlock()
	return id, isJS
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
//     plugin is called once per generation, not per request. Concurrent misses
//     for one provider share one call (single flight).
//   - The whole provider group shares one pluginThemeInjectBudget.
//   - Fragments are output raw, in provider order, head after head and footer
//     after footer. The caller (applyPublicSEO) appends them to the admin's
//     code injection and applies the commerce-page rule itself.
//   - A provider that fails, times out, returns something that is not an
//     object of strings, or whose head or footer exceeds pluginThemeInjectMax
//     contributes nothing (the whole provider on an invalid shape; only the
//     oversized slot on a size breach), is logged once with
//     pluginExtensionLog(id, "error", …) and is not retried for
//     pluginExtensionNegativeTTL. A later failure does not overwrite a fresh
//     success of the same generation.
//   - An unavailable provider is skipped without caching. A nil db, a nil
//     runtime, no active provider, or a request with no time left yields an
//     empty value and calls nothing (cache hits are still served).
func pluginThemeInjectionFor(ctx context.Context, db *gorm.DB) pluginThemeInjection {
	if currentExtensionRuntime() == nil || db == nil {
		return pluginThemeInjection{}
	}
	ids := extensionActiveProviders(ctx, db, ext.PointThemeInject)
	if len(ids) == 0 {
		return pluginThemeInjection{}
	}
	skip := pluginCallBudget(ctx, pluginThemeInjectBudget) <= 0
	callCtx, cancel := pluginCallContext(ctx, pluginThemeInjectBudget)
	defer cancel()
	var head, footer strings.Builder
	for _, id := range ids {
		h, f, ok := pluginThemeInjectFor(ctx, callCtx, skip, db, id)
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

// pluginThemeInjectFlight lets concurrent misses for one key wait for the leader.
type pluginThemeInjectFlight struct {
	done  chan struct{}
	entry pluginThemeInjectEntry
}

var pluginThemeInjectCache struct {
	sync.Mutex
	entries map[pluginThemeInjectKey]pluginThemeInjectEntry
	flights map[pluginThemeInjectKey]*pluginThemeInjectFlight
}

// pluginCacheBound is the map size above which a cache map drops stale entries
// and, if still over it, is rebuilt.
const pluginCacheBound = 4096

// pluginThemeInjectFor returns the head and footer fragments of one provider
// for the current generation, calling it at most once per generation (and
// once per negative TTL after a failure). ok is false when the provider
// contributes nothing. reqCtx is the caller's request (a waiter gives up when
// it ends); callCtx is the group's plugin call context; skip is set when the
// request had no time left, in which case misses are not called.
func pluginThemeInjectFor(reqCtx, callCtx context.Context, skip bool, db *gorm.DB, id string) (head, footer string, ok bool) {
	key := pluginThemeInjectKey{id: id, gen: pluginExtensionGeneration()}
	cache := &pluginThemeInjectCache
	cache.Lock()
	if entry, hit := cache.entries[key]; hit && pluginEntryFresh(entry.at, entry.failed) {
		cache.Unlock()
		return entry.head, entry.footer, !entry.failed
	}
	if flight, inFlight := cache.flights[key]; inFlight {
		cache.Unlock()
		select {
		case <-flight.done:
			return flight.entry.head, flight.entry.footer, !flight.entry.failed
		case <-reqCtx.Done():
			return "", "", false
		}
	}
	if skip {
		cache.Unlock()
		return "", "", false
	}
	if cache.flights == nil {
		cache.flights = make(map[pluginThemeInjectKey]*pluginThemeInjectFlight)
	}
	flight := &pluginThemeInjectFlight{done: make(chan struct{})}
	cache.flights[key] = flight
	cache.Unlock()

	entry, store := pluginThemeInjectCall(callCtx, db, id)
	if !store {
		entry = pluginThemeInjectEntry{at: time.Now(), failed: true}
	} else {
		storePluginThemeInject(key, entry)
	}
	cache.Lock()
	delete(cache.flights, key)
	cache.Unlock()
	flight.entry = entry
	close(flight.done)
	return entry.head, entry.footer, !entry.failed
}

// pluginThemeInjectCall calls one provider once and parses its result. store is
// false when the provider is unavailable: nothing is learned and nothing cached.
func pluginThemeInjectCall(callCtx context.Context, db *gorm.DB, id string) (pluginThemeInjectEntry, bool) {
	fail := func(reason string) (pluginThemeInjectEntry, bool) {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: %s", ext.PointThemeInject, reason))
		return pluginThemeInjectEntry{at: time.Now(), failed: true}, true
	}
	if callCtx.Err() != nil {
		return fail("call budget used up")
	}
	runtime, meta, err := pluginProviderMeta(callCtx, db, id, ext.PointThemeInject)
	if err != nil {
		return pluginThemeInjectEntry{}, false
	}
	output, err := runtime.CallExtension(callCtx, meta.ID, ext.PointThemeInject, []byte("{}"))
	if err != nil {
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
	return pluginThemeInjectEntry{at: time.Now(), head: headText, footer: footerText}, true
}

// storePluginThemeInject writes one entry. A failure does not overwrite a fresh
// success of the same generation. Over pluginCacheBound, stale generations go first.
func storePluginThemeInject(key pluginThemeInjectKey, entry pluginThemeInjectEntry) {
	cache := &pluginThemeInjectCache
	cache.Lock()
	defer cache.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[pluginThemeInjectKey]pluginThemeInjectEntry)
	}
	if old, ok := cache.entries[key]; ok && entry.failed && !old.failed && pluginEntryFresh(old.at, false) {
		return
	}
	cur := pluginExtensionGeneration()
	cache.entries = prunePluginCache(cache.entries, func(k pluginThemeInjectKey, _ pluginThemeInjectEntry) bool { return k.gen != cur })
	cache.entries[key] = entry
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
//     the next. The whole chain shares one pluginContentRenderBudget.
//   - The size limit is measured on the encoded input {"post","html"} with HTML
//     not escaped (pluginEncodeInput): above pluginContentRenderMax the html is
//     returned as is, without a call, a log line or a cached failure.
//   - The final HTML is cached by (post.ID, sha256(input html), generation) in
//     an LRU of at most pluginContentRenderEntries entries and
//     pluginContentRenderBytes total bytes; a hit calls no plugin (the runtime
//     ExtensionCalls counter does not move). Concurrent misses for the same key
//     compute once (single flight). Entries also expire after
//     pluginExtensionSafetyTTL.
//   - Any provider failure (error, budget, missing or non-string html) returns the original html unchanged, logs
//     pluginExtensionLog(id, "error", …) once and caches the failure for
//     pluginExtensionNegativeTTL so the page keeps rendering without the plugin.
//   - A provider that is unavailable is skipped and the chain goes on.
//   - A nil db or runtime, no active provider, or a request with no time left
//     returns html as is without calling anything.
func pluginRenderContent(ctx context.Context, db *gorm.DB, post core.Post, html string) string {
	if currentExtensionRuntime() == nil || db == nil || len(html) > pluginContentRenderMax {
		return html
	}
	ids := extensionActiveProviders(ctx, db, ext.PointContentRender)
	if len(ids) == 0 {
		return html
	}
	input, err := pluginEncodeInput(map[string]any{"post": pluginPostSummary(post), "html": html})
	if err != nil || len(input) > pluginContentRenderMax {
		return html
	}
	gen := pluginExtensionGeneration()
	key := pluginRenderKey{postID: post.ID, sum: sha256.Sum256([]byte(html)), gen: gen}

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
	if pluginCallBudget(ctx, pluginContentRenderBudget) <= 0 {
		cache.Unlock()
		return html
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
	if err == nil {
		result = out
		entry = &pluginRenderEntry{key: key, at: time.Now(), html: out}
	} else {
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

// pluginRenderChain runs html through every available provider in order. The
// whole chain runs on one budget. On failure it returns the id of the provider
// that failed and the reason.
func pluginRenderChain(ctx context.Context, db *gorm.DB, post core.Post, html string, ids []string) (string, string, error) {
	callCtx, cancel := pluginCallContext(ctx, pluginContentRenderBudget)
	defer cancel()
	summary := pluginPostSummary(post)
	current := html
	last := ""
	for _, id := range ids {
		if callCtx.Err() != nil {
			failed := id
			if last != "" {
				failed = last
			}
			return "", failed, errors.New("render budget used up")
		}
		runtime, meta, err := pluginProviderMeta(callCtx, db, id, ext.PointContentRender)
		if err != nil {
			continue
		}
		input, err := pluginEncodeInput(map[string]any{"post": summary, "html": current})
		if err != nil {
			return "", id, fmt.Errorf("encode input: %w", err)
		}
		if len(input) > pluginContentRenderMax {
			return "", id, fmt.Errorf("input is %d bytes, above the %d byte limit", len(input), pluginContentRenderMax)
		}
		output, err := runtime.CallExtension(callCtx, meta.ID, ext.PointContentRender, input)
		if err != nil {
			return "", id, fmt.Errorf("call failed: %w", err)
		}
		// The output is not capped here: the next provider's input is measured
		// above, and the page takes whatever the last provider returns.
		next, err := pluginHTMLOutput(output, math.MaxInt)
		if err != nil {
			return "", id, err
		}
		current = next
		last = id
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
// the call failed, or the request has no time left.
//
// Wanted results:
//   - The single provider is the first available one of the active list
//     (extensionSelectedProvider for ext.PointCommentsComponent); it is called with
//     {"post": pluginPostSummary(post)} and returns {"html": string}.
//   - The result is cached by (post.ID, generation) for pluginExtensionSafetyTTL.
//     Concurrent misses for one post share one call (single flight).
//   - A failure (error, timeout, missing or non-string html) is logged with
//     pluginExtensionLog(id, "error", …), cached for pluginExtensionNegativeTTL
//     and yields false so the theme's own block renders. A later failure does
//     not overwrite a fresh success of the same generation.
//   - The caller skips loading the comment tree when ok is true; the post page
//     shows the "留言" button when CommentsHTML is set.
func pluginCommentsComponent(ctx context.Context, db *gorm.DB, post core.Post) (template.HTML, bool) {
	if currentExtensionRuntime() == nil || db == nil {
		return "", false
	}
	id, isJS := extensionSelectedProvider(ctx, db, ext.PointCommentsComponent)
	if !isJS || id == extensionBuiltinID {
		return "", false
	}
	key := pluginCommentsKey{postID: post.ID, gen: pluginExtensionGeneration()}
	cache := &pluginCommentsCache
	cache.Lock()
	if entry, hit := cache.entries[key]; hit && pluginEntryFresh(entry.at, entry.failed) {
		cache.Unlock()
		return entry.html, !entry.failed
	}
	if flight, inFlight := cache.flights[key]; inFlight {
		cache.Unlock()
		select {
		case <-flight.done:
			return flight.entry.html, !flight.entry.failed
		case <-ctx.Done():
			return "", false
		}
	}
	if pluginCallBudget(ctx, pluginCommentsBudget) <= 0 {
		cache.Unlock()
		return "", false
	}
	if cache.flights == nil {
		cache.flights = make(map[pluginCommentsKey]*pluginCommentsFlight)
	}
	flight := &pluginCommentsFlight{done: make(chan struct{})}
	cache.flights[key] = flight
	cache.Unlock()

	callCtx, cancel := pluginCallContext(ctx, pluginCommentsBudget)
	entry, store := pluginCommentsCall(callCtx, db, id, post)
	cancel()
	if !store {
		entry = pluginCommentsEntry{at: time.Now(), failed: true}
	} else {
		storePluginComments(key, entry)
	}
	cache.Lock()
	delete(cache.flights, key)
	cache.Unlock()
	flight.entry = entry
	close(flight.done)
	return entry.html, !entry.failed
}

// pluginCommentsCall calls the comments provider once. store is false when the
// provider is unavailable: nothing is learned and nothing cached.
func pluginCommentsCall(callCtx context.Context, db *gorm.DB, id string, post core.Post) (pluginCommentsEntry, bool) {
	fail := func(reason string) (pluginCommentsEntry, bool) {
		pluginExtensionLog(id, "error", fmt.Sprintf("%s: %s; builtin comments kept", ext.PointCommentsComponent, reason))
		return pluginCommentsEntry{at: time.Now(), failed: true}, true
	}
	if callCtx.Err() != nil {
		return fail("call budget used up")
	}
	runtime, meta, err := pluginProviderMeta(callCtx, db, id, ext.PointCommentsComponent)
	if err != nil {
		return pluginCommentsEntry{}, false
	}
	input, err := json.Marshal(map[string]any{"post": pluginPostSummary(post)})
	if err != nil {
		return fail(fmt.Sprintf("encode input failed: %v", err))
	}
	output, err := runtime.CallExtension(callCtx, meta.ID, ext.PointCommentsComponent, input)
	if err != nil {
		return fail(fmt.Sprintf("call failed: %v", err))
	}
	fragment, err := pluginHTMLOutput(output, pluginContentRenderMax)
	if err != nil {
		return fail(err.Error())
	}
	html := template.HTML(`<section class="comments" id="comments">` + fragment + `</section>`)
	return pluginCommentsEntry{at: time.Now(), html: html}, true
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

// pluginCommentsFlight lets concurrent misses for one post wait for the leader.
type pluginCommentsFlight struct {
	done  chan struct{}
	entry pluginCommentsEntry
}

var pluginCommentsCache struct {
	sync.Mutex
	entries map[pluginCommentsKey]pluginCommentsEntry
	flights map[pluginCommentsKey]*pluginCommentsFlight
}

// storePluginComments writes one entry. A failure does not overwrite a fresh
// success of the same generation. Over pluginCacheBound, stale generations go first.
func storePluginComments(key pluginCommentsKey, entry pluginCommentsEntry) {
	cache := &pluginCommentsCache
	cache.Lock()
	defer cache.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[pluginCommentsKey]pluginCommentsEntry)
	}
	if old, ok := cache.entries[key]; ok && entry.failed && !old.failed && pluginEntryFresh(old.at, false) {
		return
	}
	cur := pluginExtensionGeneration()
	cache.entries = prunePluginCache(cache.entries, func(k pluginCommentsKey, _ pluginCommentsEntry) bool { return k.gen != cur })
	cache.entries[key] = entry
}
