package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Content event tests (docs/tickets/28-plugin-events.md, 文章與頁面 rows of the
// acceptance table). Every request goes through the real HTTP stack; every
// assertion waits for the runtime to flush before it reads a payload.
//
// Counts are cumulative unless a test calls h.reset. pecExactly flushes, waits
// a short settle so log lines still in transit land, and then fails when the
// count is not exactly n.

const pecSettle = 150 * time.Millisecond

func pecJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(raw)
}

// pecCreatePost creates a post or page through POST /api/admin/posts and
// returns its id.
func pecCreatePost(t *testing.T, h *pluginEventsHarness, token string, fields map[string]any) uint {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/posts", pecJSON(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create post")
	var got struct {
		ID uint `json:"id"`
	}
	if r.code < 200 || r.code >= 300 || json.Unmarshal([]byte(r.body), &got) != nil || got.ID == 0 {
		t.Fatalf("create post %v returned %d body=%q", fields["slug"], r.code, r.body)
	}
	return got.ID
}

// pecUpdatePost sends PUT /api/admin/posts/:id with fields and requires 200.
func pecUpdatePost(t *testing.T, h *pluginEventsHarness, token string, id uint, fields map[string]any) {
	t.Helper()
	r := h.request(http.MethodPut, "/api/admin/posts/"+pecID(id), pecJSON(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "update post")
	if r.code != http.StatusOK {
		t.Fatalf("PUT /api/admin/posts/%d returned %d body=%q", id, r.code, r.body)
	}
}

// pecAdminDo sends an admin request with no body and requires a 2xx status.
func pecAdminDo(t *testing.T, h *pluginEventsHarness, method, path, token string) accessResult {
	t.Helper()
	r := h.request(method, path, "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, method+" "+path)
	if r.code < 200 || r.code >= 300 {
		t.Fatalf("%s %s returned %d body=%q", method, path, r.code, r.body)
	}
	return r
}

func pecID(id uint) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}

// pecExactly waits until at least n OnPostSave-style deliveries of hook exist,
// settles briefly, and fails unless exactly n were captured in total.
func pecExactly(t *testing.T, h *pluginEventsHarness, hook plugin.Hook, n int) []pluginEvent {
	t.Helper()
	h.waitEvents(t, hook, n)
	time.Sleep(pecSettle)
	h.flush(t)
	got := h.events(t, pluginEventsFixtureID, hook)
	if len(got) != n {
		t.Fatalf("expected exactly %d %s deliveries, got %d: %+v\ncaptured log:\n%s", n, hook, len(got), got, h.sink.dump())
	}
	return got
}

// pecCheckBase checks the fields every payload carries.
func pecCheckBase(t *testing.T, payload map[string]any, name string) {
	t.Helper()
	if payload["event"] != name {
		t.Fatalf("payload event = %v, want %s", payload["event"], name)
	}
	at, ok := payload["at"].(string)
	if !ok {
		t.Fatalf("payload has no at string: %v", payload)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Fatalf("payload at %q is not RFC3339: %v", at, err)
	}
}

// pecCheckActor checks the actor object of an admin action.
func pecCheckActor(t *testing.T, payload map[string]any, adminID uint, username string) {
	t.Helper()
	actor := nested(t, payload, "actor")
	if number(t, actor, "id") != float64(adminID) {
		t.Fatalf("actor.id = %v, want %d", actor["id"], adminID)
	}
	if actor["role"] != "admin" {
		t.Fatalf("actor.role = %v, want admin", actor["role"])
	}
	if actor["username"] != username {
		t.Fatalf("actor.username = %v, want %s", actor["username"], username)
	}
}

// pecArray returns payload[key] as an array; a null or missing value fails.
func pecArray(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()
	value, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("%s is not an array: %v", key, payload[key])
	}
	return value
}

// pecPostID returns the post.id of a payload.
func pecPostID(t *testing.T, payload map[string]any) float64 {
	t.Helper()
	return number(t, nested(t, payload, "post"), "id")
}

// pecWaitPast sleeps until the wall clock is safely past at.
func pecWaitPast(at time.Time) {
	for time.Now().Before(at.Add(100 * time.Millisecond)) {
		time.Sleep(50 * time.Millisecond)
	}
}

// pecPublishDue runs publishDuePosts with now and requires want emitted posts.
func pecPublishDue(t *testing.T, h *pluginEventsHarness, now time.Time, want int) {
	t.Helper()
	got, err := publishDuePosts(context.Background(), h.db, now)
	if err != nil {
		t.Fatalf("publishDuePosts: %v", err)
	}
	if got != want {
		t.Fatalf("publishDuePosts returned %d, want %d", got, want)
	}
}

// pecUseActiveTheme gives the harness a theme manager with the default theme
// active, so public pages render. The shared harness only builds a manager
// with no active theme, which answers 500 "no active theme". The same runtime
// is passed again, as archivesHarness does for its own manager.
func pecUseActiveTheme(t *testing.T, h *pluginEventsHarness) {
	t.Helper()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	h.rebuildRoutes(t, tm)
}

// pecGet issues an anonymous public GET or HEAD.
func pecGet(h *pluginEventsHarness, method, path string) accessResult {
	return h.request(method, path, "", "", "text/html", "")
}

func TestPluginEventsContentDraftCreate(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-draft-admin")
	token := h.token(admin)
	h.reset(t)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Draft one", "slug": "pec-draft-one", "content": "<p>draft</p>", "type": "post", "status": "draft"})

	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	payload := saves[0].Payload
	pecCheckBase(t, payload, "OnPostSave")
	if payload["action"] != "create" {
		t.Fatalf("action = %v, want create", payload["action"])
	}
	post := nested(t, payload, "post")
	if number(t, post, "id") != float64(id) {
		t.Fatalf("post.id = %v, want %d", post["id"], id)
	}
	if post["title"] != "Draft one" || post["slug"] != "pec-draft-one" || post["status"] != "draft" || post["type"] != "post" {
		t.Fatalf("post summary = %v", post)
	}
	if number(t, post, "author_id") != float64(admin.ID) {
		t.Fatalf("post.author_id = %v, want %d", post["author_id"], admin.ID)
	}
	pecArray(t, post, "categories")
	pecArray(t, post, "tags")
	pecCheckActor(t, payload, admin.ID, admin.Username)
	for _, key := range []string{"author", "password"} {
		if _, ok := post[key]; ok {
			t.Fatalf("post object must not carry %q: %v", key, post)
		}
	}
	h.requireNoEvent(t, plugin.HookOnPostPublish)
}

func TestPluginEventsContentPublishTransitions(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-transition-admin")
	token := h.token(admin)
	h.reset(t)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Growing", "slug": "pec-growing", "content": "<p>one</p>", "type": "post", "status": "draft"})
	pecExactly(t, h, plugin.HookOnPostSave, 1)
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	pecUpdatePost(t, h, token, id, map[string]any{"status": "published"})
	saves := pecExactly(t, h, plugin.HookOnPostSave, 2)
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if saves[1].Payload["action"] != "update" {
		t.Fatalf("second OnPostSave action = %v, want update", saves[1].Payload["action"])
	}
	pecCheckBase(t, publishes[0].Payload, "OnPostPublish")
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	if pecPostID(t, publishes[0].Payload) != float64(id) {
		t.Fatalf("OnPostPublish post.id = %v, want %d", pecPostID(t, publishes[0].Payload), id)
	}

	// Ordering: the save that made the post public is announced before the publish.
	all := h.allEvents(t, pluginEventsFixtureID)
	saveAt, publishAt := -1, -1
	for i, e := range all {
		if e.Hook == string(plugin.HookOnPostSave) && e.Payload["action"] == "update" && saveAt < 0 {
			saveAt = i
		}
		if e.Hook == string(plugin.HookOnPostPublish) && publishAt < 0 {
			publishAt = i
		}
	}
	if saveAt < 0 || publishAt < 0 || saveAt >= publishAt {
		t.Fatalf("OnPostSave(update) at %d must come before OnPostPublish at %d", saveAt, publishAt)
	}

	pecUpdatePost(t, h, token, id, map[string]any{"title": "Growing, retitled"})
	saves = pecExactly(t, h, plugin.HookOnPostSave, 3)
	if saves[2].Payload["action"] != "update" {
		t.Fatalf("title-only save action = %v, want update", saves[2].Payload["action"])
	}
	if nested(t, saves[2].Payload, "post")["title"] != "Growing, retitled" {
		t.Fatalf("title-only save post.title = %v", nested(t, saves[2].Payload, "post")["title"])
	}
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
}

func TestPluginEventsContentDirectPublishedCreate(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-direct-admin")
	token := h.token(admin)
	h.reset(t)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Live now", "slug": "pec-live-now", "content": "<p>live</p>", "type": "post", "status": "published"})

	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	if saves[0].Payload["action"] != "create" {
		t.Fatalf("action = %v, want create", saves[0].Payload["action"])
	}
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	pecCheckBase(t, publishes[0].Payload, "OnPostPublish")
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	if pecPostID(t, publishes[0].Payload) != float64(id) {
		t.Fatalf("OnPostPublish post.id = %v, want %d", pecPostID(t, publishes[0].Payload), id)
	}
}

func TestPluginEventsContentPageSaves(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-page-admin")
	token := h.token(admin)
	h.reset(t)

	id := pecCreatePost(t, h, token, map[string]any{"title": "About us", "slug": "pec-about", "content": "<p>about</p>", "type": "page", "status": "draft"})
	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	if saves[0].Payload["action"] != "create" {
		t.Fatalf("page create action = %v, want create", saves[0].Payload["action"])
	}
	if nested(t, saves[0].Payload, "post")["type"] != "page" {
		t.Fatalf("page create post.type = %v, want page", nested(t, saves[0].Payload, "post")["type"])
	}

	pecUpdatePost(t, h, token, id, map[string]any{"title": "About us, revised"})
	saves = pecExactly(t, h, plugin.HookOnPostSave, 2)
	if saves[1].Payload["action"] != "update" {
		t.Fatalf("page update action = %v, want update", saves[1].Payload["action"])
	}
	if nested(t, saves[1].Payload, "post")["type"] != "page" {
		t.Fatalf("page update post.type = %v, want page", nested(t, saves[1].Payload, "post")["type"])
	}
}

func TestPluginEventsContentScheduledPublish(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-schedule-admin")
	token := h.token(admin)

	// The first run only initialises the watermark and emits nothing.
	pecPublishDue(t, h, time.Now(), 0)
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	scheduledAt := time.Now().Add(2 * time.Second)
	scheduled := pecCreatePost(t, h, token, map[string]any{"title": "Later", "slug": "pec-later", "content": "<p>later</p>", "type": "post", "status": "published", "published_at": scheduledAt})
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	// A post that goes live at once must not be announced again by the sweep.
	immediate := pecCreatePost(t, h, token, map[string]any{"title": "Now", "slug": "pec-now", "content": "<p>now</p>", "type": "post", "status": "published"})
	immediateEvents := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if pecPostID(t, immediateEvents[0].Payload) != float64(immediate) {
		t.Fatalf("immediate publish post.id = %v, want %d", pecPostID(t, immediateEvents[0].Payload), immediate)
	}

	pecWaitPast(scheduledAt)
	pecPublishDue(t, h, time.Now(), 1)
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 2)
	due := publishes[1].Payload
	pecCheckBase(t, due, "OnPostPublish")
	if due["trigger"] != "schedule" {
		t.Fatalf("scheduled OnPostPublish trigger = %v, want schedule", due["trigger"])
	}
	if actor, ok := due["actor"]; !ok || actor != nil {
		t.Fatalf("scheduled OnPostPublish actor = %v (present %v), want null", actor, ok)
	}
	if pecPostID(t, due) != float64(scheduled) {
		t.Fatalf("scheduled OnPostPublish post.id = %v, want %d", pecPostID(t, due), scheduled)
	}

	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 2)
}

func TestPluginEventsContentScheduledWindowSaveFires(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-window-admin")
	token := h.token(admin)

	pecPublishDue(t, h, time.Now(), 0)

	scheduledAt := time.Now().Add(2 * time.Second)
	id := pecCreatePost(t, h, token, map[string]any{"title": "Window", "slug": "pec-window", "content": "<p>window</p>", "type": "post", "status": "published", "published_at": scheduledAt})
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	// The time arrives, the sweep has not run, and an editor saves the post.
	pecWaitPast(scheduledAt)
	pecUpdatePost(t, h, token, id, map[string]any{"title": "Window, edited"})
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("window OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	if pecPostID(t, publishes[0].Payload) != float64(id) {
		t.Fatalf("window OnPostPublish post.id = %v, want %d", pecPostID(t, publishes[0].Payload), id)
	}

	// The later sweep must not announce it a second time.
	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
}

func TestPluginEventsContentPublicView(t *testing.T) {
	h := newPluginEventsHarness(t)
	pecUseActiveTheme(t, h)
	admin := h.user("admin", "pec-view-admin")
	token := h.token(admin)
	postID := pecCreatePost(t, h, token, map[string]any{"title": "Public post", "slug": "pec-public-post", "content": "<p>visible</p>", "type": "post", "status": "published"})
	pageID := pecCreatePost(t, h, token, map[string]any{"title": "Public page", "slug": "pec-public-page", "content": "<p>page</p>", "type": "page", "status": "published"})
	pecCreatePost(t, h, token, map[string]any{"title": "Hidden draft", "slug": "pec-hidden-draft", "content": "<p>draft</p>", "type": "post", "status": "draft"})
	h.reset(t)

	r := pecGet(h, http.MethodGet, "/post/pec-public-post")
	h.mustNoPanic(r, "public GET post")
	if r.code != http.StatusOK {
		t.Fatalf("GET /post/pec-public-post returned %d, want 200", r.code)
	}
	views := pecExactly(t, h, plugin.HookOnPostView, 1)
	payload := views[0].Payload
	pecCheckBase(t, payload, "OnPostView")
	post := nested(t, payload, "post")
	if number(t, post, "id") != float64(postID) || post["slug"] != "pec-public-post" || post["type"] != "post" {
		t.Fatalf("OnPostView post = %v", post)
	}
	if payload["path"] != "/post/pec-public-post" {
		t.Fatalf("OnPostView path = %v", payload["path"])
	}
	if payload["signed_in"] != false {
		t.Fatalf("OnPostView signed_in = %v, want false", payload["signed_in"])
	}

	headReply := pecGet(h, http.MethodHead, "/post/pec-public-post")
	h.mustNoPanic(headReply, "public HEAD post")
	pecExactly(t, h, plugin.HookOnPostView, 1)

	missing := pecGet(h, http.MethodGet, "/post/pec-no-such-post")
	h.mustNoPanic(missing, "public GET missing post")
	pecExactly(t, h, plugin.HookOnPostView, 1)

	page := pecGet(h, http.MethodGet, "/pec-public-page")
	h.mustNoPanic(page, "public GET page")
	if page.code != http.StatusOK {
		t.Fatalf("GET /pec-public-page returned %d, want 200", page.code)
	}
	views = pecExactly(t, h, plugin.HookOnPostView, 2)
	pagePayload := views[1].Payload
	if number(t, nested(t, pagePayload, "post"), "id") != float64(pageID) || nested(t, pagePayload, "post")["type"] != "page" {
		t.Fatalf("page OnPostView post = %v", nested(t, pagePayload, "post"))
	}
}

func TestPluginEventsContentTrashRestorePurge(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-trash-admin")
	token := h.token(admin)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Bin me", "slug": "pec-bin-me", "content": "<p>bin</p>", "type": "post", "status": "draft"})
	h.reset(t)

	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(id), token)
	trashed := pecExactly(t, h, plugin.HookOnPostDelete, 1)
	pecCheckBase(t, trashed[0].Payload, "OnPostDelete")
	if trashed[0].Payload["action"] != "trash" {
		t.Fatalf("trash action = %v, want trash", trashed[0].Payload["action"])
	}
	if number(t, nested(t, trashed[0].Payload, "post"), "id") != float64(id) || nested(t, trashed[0].Payload, "post")["title"] != "Bin me" {
		t.Fatalf("trash post = %v", nested(t, trashed[0].Payload, "post"))
	}

	pecAdminDo(t, h, http.MethodPost, "/api/admin/trash/post/"+pecID(id)+"/restore", token)
	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	if saves[0].Payload["action"] != "restore" {
		t.Fatalf("restore OnPostSave action = %v, want restore", saves[0].Payload["action"])
	}
	pecCheckActor(t, saves[0].Payload, admin.ID, admin.Username)

	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(id), token)
	pecExactly(t, h, plugin.HookOnPostDelete, 2)
	pecAdminDo(t, h, http.MethodDelete, "/api/admin/trash/post/"+pecID(id), token)
	deletes := pecExactly(t, h, plugin.HookOnPostDelete, 3)
	purge := deletes[2].Payload
	pecCheckBase(t, purge, "OnPostDelete")
	if purge["action"] != "purge" {
		t.Fatalf("purge action = %v, want purge", purge["action"])
	}
	summary := nested(t, purge, "post")
	if len(summary) != 4 {
		t.Fatalf("purge post has %d keys, want exactly id, type, slug, title: %v", len(summary), summary)
	}
	for _, key := range []string{"id", "type", "slug", "title"} {
		if _, ok := summary[key]; !ok {
			t.Fatalf("purge post lacks %q: %v", key, summary)
		}
	}
	// The second trash rewrote the slug to "<slug>~<id>", so only the prefix is fixed.
	slug, _ := summary["slug"].(string)
	if number(t, summary, "id") != float64(id) || !strings.HasPrefix(slug, "pec-bin-me") {
		t.Fatalf("purge post = %v", summary)
	}
}

func TestPluginEventsContentRevisionRestore(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-revision-admin")
	token := h.token(admin)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Versioned", "slug": "pec-versioned", "content": "<p>first body</p>", "type": "post", "status": "draft"})
	pecUpdatePost(t, h, token, id, map[string]any{"content": "<p>second body</p>"})
	h.reset(t)

	list := pecAdminDo(t, h, http.MethodGet, "/api/admin/posts/"+pecID(id)+"/revisions", token)
	var revs []struct {
		ID   uint   `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(list.body), &revs); err != nil || len(revs) < 2 {
		t.Fatalf("expected at least two revisions, got %q (err %v)", list.body, err)
	}
	oldest := revs[len(revs)-1].ID // the list is newest first
	for _, rev := range revs {
		if rev.ID < oldest {
			oldest = rev.ID
		}
	}

	pecAdminDo(t, h, http.MethodPost, "/api/admin/posts/"+pecID(id)+"/revisions/"+pecID(oldest)+"/restore", token)
	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	if saves[0].Payload["action"] != "revision" {
		t.Fatalf("revision restore OnPostSave action = %v, want revision", saves[0].Payload["action"])
	}
	if number(t, nested(t, saves[0].Payload, "post"), "id") != float64(id) {
		t.Fatalf("revision restore post.id = %v, want %d", nested(t, saves[0].Payload, "post")["id"], id)
	}
	pecCheckActor(t, saves[0].Payload, admin.ID, admin.Username)
}

func TestPluginEventsContentLargeContentOmitted(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-large-admin")
	token := h.token(admin)
	h.reset(t)

	// globalBodyLimit is 2M, so a body just over 256 KiB reaches the handler.
	large := strings.Repeat("a", pluginEventContentLimit+1)
	pecCreatePost(t, h, token, map[string]any{"title": "Large", "slug": "pec-large", "content": large, "type": "post", "status": "draft"})
	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	largePost := nested(t, saves[0].Payload, "post")
	if _, ok := largePost["content"]; ok {
		t.Fatalf("oversized post must not carry content, got keys %v", largePost)
	}
	if largePost["content_omitted"] != true {
		t.Fatalf("oversized post content_omitted = %v, want true", largePost["content_omitted"])
	}

	pecCreatePost(t, h, token, map[string]any{"title": "Small", "slug": "pec-small", "content": "<p>small</p>", "type": "post", "status": "draft"})
	saves = pecExactly(t, h, plugin.HookOnPostSave, 2)
	smallPost := nested(t, saves[1].Payload, "post")
	if _, ok := smallPost["content"].(string); !ok {
		t.Fatalf("normal post content missing: %v", smallPost)
	}
	if omitted, ok := smallPost["content_omitted"]; ok && omitted != false {
		t.Fatalf("normal post content_omitted = %v, want absent or false", omitted)
	}
}

func TestPluginEventsContentWithoutEventsPermission(t *testing.T) {
	h := newPluginEventsHarness(t)
	h.reset(t)
	h.installFixturePluginWithPermission(t, "no-perm", nil, false)
	admin := h.user("admin", "pec-noperm-admin")
	token := h.token(admin)

	pecCreatePost(t, h, token, map[string]any{"title": "Guarded", "slug": "pec-guarded", "content": "<p>guarded</p>", "type": "post", "status": "draft"})
	pecExactly(t, h, plugin.HookOnPostSave, 1)
	h.flush(t)

	if got := h.events(t, "no-perm", plugin.HookOnPostSave); len(got) != 0 {
		t.Fatalf("plugin without events permission received %d OnPostSave: %+v", len(got), got)
	}

	// The plugin still loaded: its fixture-ready line reached the sink.
	deadline := time.Now().Add(5 * time.Second)
	for {
		found := false
		for _, line := range h.sink.snapshot() {
			if line.pluginID == "no-perm" && line.message == "fixture-ready" {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no-perm never logged fixture-ready; captured log:\n%s", h.sink.dump())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPluginEventsContentAdminLogsTab(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pec-logs-admin")
	token := h.token(admin)

	pecCreatePost(t, h, token, map[string]any{"title": "Logged", "slug": "pec-logged", "content": "<p>logged</p>", "type": "post", "status": "draft"})
	h.waitEvents(t, plugin.HookOnPostSave, 1)

	deadline := time.Now().Add(5 * time.Second)
	for {
		r := h.request(http.MethodGet, "/api/admin/plugins/"+pluginEventsFixtureID+"/logs", "", token, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(r, "plugin logs")
		if r.code != http.StatusOK {
			t.Fatalf("GET plugin logs returned %d body=%q, want 200", r.code, r.body)
		}
		var rows []struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(r.body), &rows); err != nil {
			t.Fatalf("decode plugin logs %q: %v", r.body, err)
		}
		for _, row := range rows {
			if strings.HasPrefix(row.Message, "event:OnPostSave:") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("admin logs tab has no event:OnPostSave: row; body=%q\ncaptured log:\n%s", r.body, h.sink.dump())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
