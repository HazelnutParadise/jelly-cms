package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/events"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Plugin event bridge (docs/tickets/27-plugin-events.md).
//
// Site code stages events with stagePluginEvent inside a transaction (or
// publishes with publishPluginEvent after a standalone write). The bridge
// subscribed by configurePluginEvents turns every published event into
// runtime.Emit, and forwardPluginLog turns plugin log lines into system_logs
// rows so the admin "logs" tab shows them.

// pluginEventContentLimit is the largest post content attached to an event.
// Longer content is omitted and the payload carries content_omitted: true.
const pluginEventContentLimit = 256 << 10

// publishWatermarkOption stores the time up to which posts.publish_due has
// already emitted OnPostPublish for scheduled posts.
const publishWatermarkOption = "plugin_events_publish_watermark"

// pluginEventPostTypes are the post types that emit content events and that
// the scheduled publish sweep considers.
var pluginEventPostTypes = []string{"post", "page"}

// pluginEventPermission is the manifest permission a plugin must declare and
// have approved to receive site events.
const pluginEventPermission = "events"

// pluginEventsPermissionTTL is how long a plugin's event permission verdict is
// reused before the plugins row is read again.
const pluginEventsPermissionTTL = 5 * time.Second

// pluginLogQueueSize bounds the plugin log lines waiting for the database writer.
const pluginLogQueueSize = 1024

// pluginEventReadTimeout caps each database read made on the publishing
// goroutine while a plugin event is enriched or its permission is checked. The
// read runs after commit and before the HTTP response is sent, so a slow
// database delays a response by at most this long per read.
const pluginEventReadTimeout = 2 * time.Second

// pluginEventsSweepLimit bounds the posts one publish_due sweep announces. A
// full page moves the watermark to its last published_at so the next sweep
// continues from there.
const pluginEventsSweepLimit = 500

// pluginEventsSubscription holds the cancel function of the bridge subscription
// installed by the latest configurePluginEvents call.
var pluginEventsSubscription = struct {
	sync.Mutex
	cancel func()
}{}

// pluginEventsPermissionCache remembers the event permission verdict per plugin.
var pluginEventsPermissionCache = struct {
	sync.Mutex
	entries map[string]pluginEventsPermissionEntry
}{entries: map[string]pluginEventsPermissionEntry{}}

type pluginEventsPermissionEntry struct {
	allowed   bool
	checkedAt time.Time
}

// pluginLogRecord is one plugin log line waiting for the system log writer.
type pluginLogRecord struct {
	pluginID string
	level    string
	message  string
}

// pluginLogWriter owns the bounded channel and the single goroutine that
// drains it into syslog. The goroutine starts on the first forwarded line.
var pluginLogWriter = struct {
	once  sync.Once
	queue chan pluginLogRecord
}{queue: make(chan pluginLogRecord, pluginLogQueueSize)}

// configurePluginEvents subscribes the bridge for runtime, installs the log
// sink and the event filter. Calling it again replaces the previous
// subscription, so a test that builds several runtimes only feeds the newest
// one. A nil runtime removes the subscription. Wanted results:
//   - every events.Publish reaches runtime.Emit with Hook(e.Name) and a payload
//     that is e.Payload plus "event" (name) and "at" (RFC3339 UTC), after
//     enrichPluginEvent filled in "order" and "actor" for thin events
//   - the subscriber never blocks on the runtime (Emit is non-blocking); the
//     enrichment reads happen on the publishing goroutine after commit
//   - runtime.SetEventFilter(pluginEventsAllowed)
//   - plugin.SetLogSink(forwardPluginLog)
func configurePluginEvents(runtime *plugin.Runtime) {
	pluginEventsSubscription.Lock()
	defer pluginEventsSubscription.Unlock()

	if pluginEventsSubscription.cancel != nil {
		pluginEventsSubscription.cancel()
		pluginEventsSubscription.cancel = nil
	}
	// A new runtime starts with no verdicts; a permission cached for the old
	// runtime or database must not decide for the new one.
	resetPluginEventsPermissionCache()
	if runtime == nil {
		return
	}

	pluginEventsSubscription.cancel = events.Subscribe(func(e events.Event) {
		if e.Name == "" {
			return
		}
		hook := plugin.Hook(e.Name)
		// Skip enrichment reads when no loaded plugin listens for the hook.
		if !runtimeWantsHook(runtime, hook) {
			return
		}
		payload := make(map[string]any, len(e.Payload)+2)
		for key, value := range e.Payload {
			payload[key] = value
		}
		enrichPluginEvent(payload)
		payload["event"] = e.Name
		payload["at"] = e.At.UTC().Format(time.RFC3339)
		runtime.Emit(hook, payload)
	})
	runtime.SetEventFilter(pluginEventsAllowed)
	plugin.SetLogSink(forwardPluginLog)
}

// resetPluginEventsPermissionCache forgets every cached permission verdict.
func resetPluginEventsPermissionCache() {
	pluginEventsPermissionCache.Lock()
	pluginEventsPermissionCache.entries = map[string]pluginEventsPermissionEntry{}
	pluginEventsPermissionCache.Unlock()
}

// runtimeWantsHook reports whether any loaded plugin of runtime registered hook.
func runtimeWantsHook(runtime *plugin.Runtime, hook plugin.Hook) bool {
	if runtime == nil {
		return false
	}
	for _, meta := range runtime.LoadedPlugins() {
		if runtime.HasHook(meta.ID, hook) {
			return true
		}
	}
	return false
}

// enrichPluginEvent completes a thin event payload in place: when it has a
// numeric "order_id" and no "order", the order summary (orderEventPayload
// without items) is loaded from data.DB; when it has "actor_user_id" and no
// "actor", pluginEventActorID fills "actor" (null when missing). A missing
// order leaves "order" null. Errors are logged and never stop dispatch.
func enrichPluginEvent(payload map[string]any) {
	if payload == nil {
		return
	}
	if raw, has := payload["order_id"]; has {
		delete(payload, "order_id")
		if _, exists := payload["order"]; !exists {
			payload["order"] = pluginEventOrderSummary(data.DB, raw)
		}
	}
	if raw, has := payload["actor_user_id"]; has {
		delete(payload, "actor_user_id")
		if _, exists := payload["actor"]; !exists {
			id, _ := pluginEventUint(raw)
			payload["actor"] = pluginEventActorID(data.DB, id)
		}
	}
}

// pluginEventReadDB binds db to a context that expires after
// pluginEventReadTimeout. The caller must call the returned cancel function.
func pluginEventReadDB(db *gorm.DB) (*gorm.DB, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginEventReadTimeout)
	return db.WithContext(ctx), cancel
}

// pluginEventOrderSummary loads the order named by raw for a thin event. It
// returns nil when raw is not a positive id, db is nil or the order is gone.
func pluginEventOrderSummary(db *gorm.DB, raw any) map[string]any {
	id, ok := pluginEventUint(raw)
	if !ok || db == nil {
		return nil
	}
	db, cancel := pluginEventReadDB(db)
	defer cancel()
	var order core.Order
	if err := db.First(&order, id).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("plugin events: load order %d: %v", id, err)
		}
		return nil
	}
	return orderEventPayload(order, nil)
}

// pluginEventUint converts the numeric forms a payload can carry into a
// positive uint. Anything else, including zero, reports false.
func pluginEventUint(raw any) (uint, bool) {
	switch v := raw.(type) {
	case uint:
		return v, v > 0
	case uint64:
		return uint(v), v > 0 && uint64(uint(v)) == v
	case int:
		return uint(v), v > 0
	case int64:
		return uint(v), v > 0 && int64(uint(v)) == v
	case float64:
		if v >= 1 && v <= math.MaxUint32 && v == math.Trunc(v) {
			return uint(v), true
		}
		return 0, false
	case json.Number:
		parsed, err := strconv.ParseUint(v.String(), 10, 64)
		if err != nil || parsed == 0 {
			return 0, false
		}
		return uint(parsed), true
	}
	return 0, false
}

// pluginEventsAllowed is the event filter: a plugin receives site events only
// when its plugins row is enabled, permissions_granted_hash equals
// plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta)) and meta
// declares the "events" permission. Results are cached per plugin id for five
// seconds; a database error or missing row denies. OnBoot, OnShutdown and
// action hooks never reach the filter.
func pluginEventsAllowed(meta plugin.PluginMetadata, hook plugin.Hook) bool {
	db := data.DB
	if db == nil || !pluginEventsManifestDeclared(meta) {
		return false
	}

	now := time.Now()
	pluginEventsPermissionCache.Lock()
	entry, cached := pluginEventsPermissionCache.entries[meta.ID]
	pluginEventsPermissionCache.Unlock()
	if cached && now.Sub(entry.checkedAt) < pluginEventsPermissionTTL {
		return entry.allowed
	}

	allowed := pluginEventsDatabaseAllows(db, meta)
	pluginEventsPermissionCache.Lock()
	pluginEventsPermissionCache.entries[meta.ID] = pluginEventsPermissionEntry{allowed: allowed, checkedAt: now}
	pluginEventsPermissionCache.Unlock()
	return allowed
}

// pluginEventsManifestDeclared reports whether meta declares the events permission.
func pluginEventsManifestDeclared(meta plugin.PluginMetadata) bool {
	for _, permission := range meta.Permissions {
		if permission.Name == pluginEventPermission {
			return true
		}
	}
	return false
}

// pluginEventsDatabaseAllows reads the plugins row of meta and checks that it
// is enabled and that its approval matches the manifest's permission names.
func pluginEventsDatabaseAllows(db *gorm.DB, meta plugin.PluginMetadata) bool {
	db, cancel := pluginEventReadDB(db)
	defer cancel()
	var row struct {
		Enabled bool   `gorm:"column:enabled"`
		Granted string `gorm:"column:permissions_granted_hash"`
	}
	if err := db.Table("plugins").Select("enabled, permissions_granted_hash").Where("id = ?", meta.ID).Take(&row).Error; err != nil {
		return false
	}
	if !row.Enabled || row.Granted == "" {
		return false
	}
	return row.Granted == plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
}

// pluginEventsWanted reports whether any loaded plugin registered hook, so hot
// paths (public views) can skip building a payload nobody will receive. It
// reports false without a runtime.
func pluginEventsWanted(hook plugin.Hook) bool {
	return runtimeWantsHook(currentExtensionRuntime(), hook)
}

// forwardPluginLog hands one plugin log line to a bounded channel (1024) whose
// single writer goroutine calls syslog.Emit with PluginID set, Source "plugin"
// and the level mapped to syslog levels (debug and info → LevelInfo, warn →
// LevelWarn, error → LevelError). It never blocks the caller and never panics;
// a full channel drops the line, and a nil data.DB lets syslog drop it.
func forwardPluginLog(pluginID, level, message string) {
	pluginLogWriter.once.Do(func() {
		go drainPluginLogs(pluginLogWriter.queue)
	})
	select {
	case pluginLogWriter.queue <- pluginLogRecord{pluginID: pluginID, level: level, message: message}:
	default:
	}
}

// drainPluginLogs writes queued plugin log lines until the process exits.
func drainPluginLogs(queue <-chan pluginLogRecord) {
	for record := range queue {
		writePluginLog(record)
	}
}

// writePluginLog writes one line to syslog. A panic while writing drops the
// line instead of ending the writer goroutine.
func writePluginLog(record pluginLogRecord) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("plugin events: write plugin log for %s: %v", record.pluginID, r)
		}
	}()
	syslog.Emit(context.Background(), syslog.Entry{
		Level:    pluginLogLevel(record.level),
		Source:   syslog.SourcePlugin,
		Message:  record.message,
		PluginID: record.pluginID,
	})
}

// pluginLogLevel maps a plugin log level to a syslog level.
func pluginLogLevel(level string) int {
	switch level {
	case "error":
		return syslog.LevelError
	case "warn":
		return syslog.LevelWarn
	default:
		return syslog.LevelInfo
	}
}

// stagePluginEvent stages hook with payload on the outbox of ctx so it is
// published after the transaction commits. Without an outbox events.Stage
// drops the event and logs it; a write outside a transaction publishes with
// publishPluginEvent instead. The payload gains "event" and "at" when the
// bridge dispatches it; callers only supply the hook-specific fields.
func stagePluginEvent(ctx context.Context, hook plugin.Hook, payload map[string]any) {
	events.Stage(ctx, events.Event{Name: string(hook), Payload: payload})
}

// publishPluginEvent publishes hook with payload now. Use after a standalone
// write that has no transaction.
func publishPluginEvent(hook plugin.Hook, payload map[string]any) {
	events.Publish(events.Event{Name: string(hook), Payload: payload})
}

// pluginEventActor returns {"id", "username", "role"} for the signed-in user of
// c, or nil for anonymous and integration-key requests without a user.
func pluginEventActor(c echo.Context) map[string]any {
	if c == nil {
		return nil
	}
	user, err := currentUser(c)
	if err != nil || user == nil {
		user, err = auth.GetCurrentUser(c)
	}
	if err != nil || user == nil {
		return nil
	}
	return pluginEventActorMap(user)
}

// pluginEventActorID returns the same map for a known user id, loading the row
// from db; nil when id is 0 or the row is missing.
func pluginEventActorID(db *gorm.DB, id uint) map[string]any {
	if db == nil || id == 0 {
		return nil
	}
	db, cancel := pluginEventReadDB(db)
	defer cancel()
	var user core.User
	if err := db.Select("id", "username", "role").First(&user, id).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("plugin events: load actor %d: %v", id, err)
		}
		return nil
	}
	return pluginEventActorMap(&user)
}

// pluginEventActorMap is the actor object shared by pluginEventActor and
// pluginEventActorID.
func pluginEventActorMap(user *core.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "role": user.Role}
}

// postEventPayload builds the "post" object: id, type, slug, title, status,
// visibility, excerpt, content, format, author_id, published_at (RFC3339 or
// null), created_at, updated_at, categories [{id,name,slug}], tags
// [{id,name,slug}], pinned. Content longer than pluginEventContentLimit is
// omitted and content_omitted is true. Categories and tags are always arrays.
func postEventPayload(post core.Post) map[string]any {
	categories := make([]map[string]any, 0, len(post.Categories))
	for _, category := range post.Categories {
		categories = append(categories, map[string]any{"id": category.ID, "name": category.Name, "slug": category.Slug})
	}
	tags := make([]map[string]any, 0, len(post.Tags))
	for _, tag := range post.Tags {
		tags = append(tags, map[string]any{"id": tag.ID, "name": tag.Name, "slug": tag.Slug})
	}
	out := map[string]any{
		"id":           post.ID,
		"type":         post.Type,
		"slug":         post.Slug,
		"title":        post.Title,
		"status":       post.Status,
		"visibility":   post.Visibility,
		"excerpt":      post.Excerpt,
		"format":       post.Format,
		"author_id":    post.AuthorID,
		"published_at": pluginEventTimePtr(post.PublishedAt),
		"created_at":   pluginEventTime(post.CreatedAt),
		"updated_at":   pluginEventTime(post.UpdatedAt),
		"categories":   categories,
		"tags":         tags,
		"pinned":       post.Pinned,
	}
	if len(post.Content) > pluginEventContentLimit {
		out["content_omitted"] = true
	} else {
		out["content"] = post.Content
	}
	return out
}

// postEventSummary builds the short "post" object used by OnPostView and purge:
// id, type, slug, title, author_id.
func postEventSummary(post core.Post) map[string]any {
	return map[string]any{
		"id":        post.ID,
		"type":      post.Type,
		"slug":      post.Slug,
		"title":     post.Title,
		"author_id": post.AuthorID,
	}
}

// postIsPublic reports whether post is publicly readable at now: status is
// "published", visibility is "public" (an empty visibility counts as public),
// and published_at is set and not after now.
func postIsPublic(post core.Post, now time.Time) bool {
	if post.Status != "published" || post.PublishedAt == nil || post.PublishedAt.After(now) {
		return false
	}
	return post.Visibility == "public" || post.Visibility == ""
}

// postPublishAnnouncement reports whether a write from existing to saved makes
// the post publicly readable for the first time, so OnPostPublish (trigger
// "save") must be staged. existing is the row as it was before the write; a
// zero Post stands for a create. A post that was public but whose scheduled
// announcement is still pending (postPublishPending) counts as not yet
// announced.
func postPublishAnnouncement(db *gorm.DB, existing, saved core.Post, now time.Time) bool {
	wasPublic := postIsPublic(existing, now) && !postPublishPending(db, existing, now)
	return !wasPublic && postIsPublic(saved, now)
}

// pluginEventsPostWanted reports whether a post write can announce anything
// (OnPostSave or OnPostPublish). Handlers skip the reads that only feed those
// events when no loaded plugin listens.
func pluginEventsPostWanted() bool {
	return pluginEventsWanted(plugin.HookOnPostSave) || pluginEventsWanted(plugin.HookOnPostPublish)
}

// postPublishPending reports whether existing is a scheduled post whose time
// arrived but which posts.publish_due has not announced yet: status
// "published", published_at set, published_at <= now, published_at >
// existing.UpdatedAt (still in the future at the last save) and published_at
// after the stored watermark (publishWatermarkOption; empty means zero time).
// A save of such a post must emit OnPostPublish (trigger "save") itself,
// because the save moves updated_at past published_at and the sweep would
// skip it. It reads the option through db.
func postPublishPending(db *gorm.DB, existing core.Post, now time.Time) bool {
	if existing.Status != "published" || existing.PublishedAt == nil {
		return false
	}
	publishedAt := *existing.PublishedAt
	if publishedAt.After(now) || !publishedAt.After(existing.UpdatedAt) {
		return false
	}
	return publishedAt.After(publishWatermark(db))
}

// publishWatermark reads the publish watermark option. A missing row, an empty
// value, a read error or an unparsable value all mean the zero time.
func publishWatermark(db *gorm.DB) time.Time {
	if db == nil {
		return time.Time{}
	}
	var row core.Option
	if err := db.Where("key = ?", publishWatermarkOption).Take(&row).Error; err != nil {
		return time.Time{}
	}
	raw := strings.TrimSpace(row.Value)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// orderEventPayload builds the "order" object: id, number, status, email,
// user_id, currency, subtotal, discount_total, shipping_total, tax_total,
// total_amount, refunded_total, payment_provider, payment_method, coupon_code,
// created_at, paid_at (RFC3339 or null). When items is non-nil the object also
// carries items [{product_id, variant_id, title, variant_name, sku, quantity,
// unit_price, line_total}]. Shipping, invoice, lookup token hash and the
// idempotency key are never included.
func orderEventPayload(order core.Order, items []core.OrderItem) map[string]any {
	out := map[string]any{
		"id":               order.ID,
		"number":           order.Number,
		"status":           order.Status,
		"email":            order.Email,
		"user_id":          pluginEventUintPtr(order.UserID),
		"currency":         order.Currency,
		"subtotal":         order.Subtotal,
		"discount_total":   order.DiscountTotal,
		"shipping_total":   order.ShippingTotal,
		"tax_total":        order.TaxTotal,
		"total_amount":     order.TotalAmount,
		"refunded_total":   order.RefundedTotal,
		"payment_provider": order.PaymentProvider,
		"payment_method":   order.PaymentMethod,
		"coupon_code":      order.CouponCode,
		"created_at":       pluginEventTime(order.CreatedAt),
		"paid_at":          pluginEventTimePtr(order.PaidAt),
	}
	if items != nil {
		list := make([]map[string]any, 0, len(items))
		for _, item := range items {
			list = append(list, map[string]any{
				"product_id":   pluginEventUintPtr(item.ProductID),
				"variant_id":   pluginEventUintPtr(item.VariantID),
				"title":        item.Title,
				"variant_name": item.VariantName,
				"sku":          item.SKU,
				"quantity":     item.Quantity,
				"unit_price":   item.UnitPrice,
				"line_total":   item.LineTotal,
			})
		}
		out["items"] = list
	}
	return out
}

// productEventPayload builds the "product" object: id, slug, title, status,
// price, compare_at_price, sku, stock, kind, images (decoded JSON or empty
// array), created_at, updated_at.
func productEventPayload(product core.Product) map[string]any {
	images := []any{}
	if len(product.Images) > 0 {
		var decoded []any
		if err := json.Unmarshal(product.Images, &decoded); err == nil && decoded != nil {
			images = decoded
		}
	}
	return map[string]any{
		"id":               product.ID,
		"slug":             product.Slug,
		"title":            product.Title,
		"status":           product.Status,
		"price":            product.Price,
		"compare_at_price": product.CompareAtPrice,
		"sku":              product.SKU,
		"stock":            product.Stock,
		"kind":             product.Kind,
		"images":           images,
		"created_at":       pluginEventTime(product.CreatedAt),
		"updated_at":       pluginEventTime(product.UpdatedAt),
	}
}

// productEventSummary builds the short object for OnProductView and purge: id,
// slug, title, price, currency (the shop currency setting, "TWD" when unset).
func productEventSummary(product core.Product) map[string]any {
	return map[string]any{
		"id":       product.ID,
		"slug":     product.Slug,
		"title":    product.Title,
		"price":    product.Price,
		"currency": pluginEventShopCurrency(),
	}
}

// pluginEventShopCurrency reads the shop currency option, "TWD" when it is
// unset or cannot be read.
func pluginEventShopCurrency() string {
	if data.DB == nil {
		return "TWD"
	}
	currency, err := shopReadOptionWithDB(data.DB, "shop_currency")
	if err != nil || strings.TrimSpace(currency) == "" {
		return "TWD"
	}
	return currency
}

// userEventPayload builds the "user" object: id, username, display_name, email,
// role, status, provider, created_at. It never includes password, pending email
// hashes or avatar data.
func userEventPayload(user core.User) map[string]any {
	return map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"email":        user.Email,
		"role":         user.Role,
		"status":       user.Status,
		"provider":     user.Provider,
		"created_at":   pluginEventTime(user.CreatedAt),
	}
}

// commentEventPayload builds the "comment" object: id, post_id, parent_id,
// author_id, name, status, body, created_at. IP, user agent and email are
// never included.
func commentEventPayload(comment core.Comment) map[string]any {
	return map[string]any{
		"id":         comment.ID,
		"post_id":    comment.PostID,
		"parent_id":  pluginEventUintPtr(comment.ParentID),
		"author_id":  pluginEventUintPtr(comment.AuthorID),
		"name":       comment.Name,
		"status":     comment.Status,
		"body":       comment.Body,
		"created_at": pluginEventTime(comment.CreatedAt),
	}
}

// mediaEventPayload builds the "file" object: id, path, filename, mime, size,
// width, height, alt, policy_id, group_id, created_at.
func mediaEventPayload(row mediaFileRow) map[string]any {
	return map[string]any{
		"id":         row.ID,
		"path":       row.Path,
		"filename":   row.Filename,
		"mime":       row.Mime,
		"size":       row.Size,
		"width":      pluginEventIntPtr(row.Width),
		"height":     pluginEventIntPtr(row.Height),
		"alt":        row.Alt,
		"policy_id":  row.PolicyID,
		"group_id":   pluginEventInt64Ptr(row.GroupID),
		"created_at": pluginEventTime(row.CreatedAt),
	}
}

// pluginEventTime formats t as RFC3339 in UTC.
func pluginEventTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// pluginEventTimePtr formats t as RFC3339 in UTC, or nil when t is nil.
func pluginEventTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return pluginEventTime(*t)
}

// pluginEventUintPtr returns the value of v, or nil.
func pluginEventUintPtr(v *uint) any {
	if v == nil {
		return nil
	}
	return *v
}

// pluginEventIntPtr returns the value of v, or nil.
func pluginEventIntPtr(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// pluginEventInt64Ptr returns the value of v, or nil.
func pluginEventInt64Ptr(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// publishDuePosts emits OnPostPublish (trigger "schedule") for posts whose
// scheduled time arrived since the previous run. It runs in one transaction:
// lock the watermark option (lockMaintenanceOption with publishWatermarkOption,
// default ""), and
//   - with an empty watermark write now and emit nothing (first run never
//     replays history);
//   - with now <= watermark return without writing (the watermark never moves
//     back, so posts already announced are not announced again);
//   - otherwise select up to pluginEventsSweepLimit posts with status
//     'published', visibility public (or empty/NULL), deleted_at IS NULL, type
//     IN ('post','page'), published_at > watermark, published_at <= now and
//     published_at > updated_at, ordered by published_at, id; stage one
//     OnPostPublish per row with postEventPayload, trigger "schedule" and actor
//     null. The watermark becomes now, or the last row's published_at when the
//     page is full so the next sweep continues from it.
//
// Events are published after commit. It returns the number of posts emitted.
// The watermark is stored as RFC3339Nano UTC.
func publishDuePosts(ctx context.Context, db *gorm.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, errors.New("publish due posts: database is not ready")
	}
	now = now.UTC()
	emitted := 0
	err := events.Transaction(ctx, db, func(tx *gorm.DB) error {
		row, err := lockMaintenanceOption(tx, publishWatermarkOption, "")
		if err != nil {
			return fmt.Errorf("publish due posts: lock watermark: %w", err)
		}
		if row.Value == "" {
			return writeMaintenanceOption(tx, publishWatermarkOption, now.Format(time.RFC3339Nano))
		}
		watermark, err := time.Parse(time.RFC3339Nano, row.Value)
		if err != nil {
			return fmt.Errorf("publish due posts: parse watermark: %w", err)
		}
		if !now.After(watermark) {
			return nil
		}

		var posts []core.Post
		if err := tx.Where(
			"type IN ? AND status = ? AND (visibility = 'public' OR visibility = '' OR visibility IS NULL) AND deleted_at IS NULL AND published_at > ? AND published_at <= ? AND published_at > updated_at",
			pluginEventPostTypes, "published", watermark, now,
		).Order("published_at, id").Limit(pluginEventsSweepLimit).Preload("Categories").Preload("Tags").Find(&posts).Error; err != nil {
			return fmt.Errorf("publish due posts: select posts: %w", err)
		}
		for _, post := range posts {
			stagePluginEvent(tx.Statement.Context, plugin.HookOnPostPublish, map[string]any{
				"trigger": "schedule",
				"post":    postEventPayload(post),
				"actor":   nil,
			})
		}
		emitted = len(posts)
		next := now
		if len(posts) == pluginEventsSweepLimit && posts[len(posts)-1].PublishedAt != nil {
			next = *posts[len(posts)-1].PublishedAt
		}
		return writeMaintenanceOption(tx, publishWatermarkOption, next.Format(time.RFC3339Nano))
	})
	if err != nil {
		return 0, err
	}
	return emitted, nil
}
