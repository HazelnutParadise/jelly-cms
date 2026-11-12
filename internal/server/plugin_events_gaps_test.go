package server

// Event gap tests for ticket 29 (事件缺口 and the 驗收 rows for trash purge,
// WXR import, OAuth first login and autosave). They drive the real routes, the importer, the OAuth callback and the
// trash purge through the plugin runtime with the fixture plugin from plugin_events_harness_test.go. They are skipped
// unless the harness runs (JELLY_TEST_POSTGRES=1).
//
// TestPluginEventsGapsTrashAutoPurge and TestPluginEventsGapsWXRImport must fail on the current code: the automatic
// purge and the WXR importer do not stage events yet (AGENTS.md follow-ups). TestPluginEventsGapsOAuthFirstLogin is a
// control: it may already pass. TestPluginEventsGapsAutosaveIsNotPostSave is a control for the documented behavior that
// autosave writes only a revision. Do not edit these tests to match an implementation.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
)

// gapsWXRItem is one WXR post item with the given status (publish or draft).
func gapsWXRItem(id int, title, slug, status string) string {
	return fmt.Sprintf(`<item><title>%s</title><link>https://old.example.test/2026/01/%s/</link><dc:creator><![CDATA[alice]]></dc:creator><content:encoded><![CDATA[<p>%s body</p>]]></content:encoded><wp:post_id>%d</wp:post_id><wp:post_name>%s</wp:post_name><wp:post_type>post</wp:post_type><wp:status>%s</wp:status></item>`, title, slug, title, id, slug, status)
}

// gapsAgeRows moves the trashed row of table with id to 400 days before now, well past the retention window.
func gapsAgeRows(t *testing.T, h *pluginEventsHarness, table string, id uint) {
	t.Helper()
	old := time.Now().Add(-400 * 24 * time.Hour)
	if err := h.db.Exec(fmt.Sprintf("UPDATE %s SET deleted_at = ? WHERE id = ?", table), old, id).Error; err != nil {
		t.Fatalf("age trashed %s %d: %v", table, id, err)
	}
}

func TestPluginEventsGapsTrashAutoPurge(t *testing.T) {
	h := pluginEventsShopHarness(t)
	adm, admin := pluginEventsShopAdmin(t, h, "gaps-purge-admin")
	token := h.token(admin)
	editor := h.user("editor", "gaps-purge-editor")
	editorToken := h.token(editor)

	postID := pecCreatePost(t, h, token, map[string]any{"title": "Gaps purge post", "slug": "gaps-purge-post", "content": "<p>bin</p>", "type": "post", "status": "draft"})
	product := shopSeedProduct(t, h.accessHarness, "gaps-purge-product", 1000, 3)
	commentPost := commentsPublishedPost(t, h.accessHarness, editor, "gaps-purge-comment-post", nil)
	comment := commentsSeed(t, h.accessHarness, commentsRow(commentPost.ID, "GapsPurge", "pending", 1))

	// Move all three rows to the bin through their normal admin routes.
	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(postID), token)
	shopWantOK(t, adm.send(http.MethodDelete, fmt.Sprintf("/api/admin/products/%d", product.ID), nil), "DELETE /api/admin/products/:id")
	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", comment.ID), "", editorToken, ""); !commentsOK(t, r, "trash comment", http.StatusOK) {
		return
	}

	// Backdate the bin entries past the retention window, then run the automatic purge.
	gapsAgeRows(t, h, "posts", postID)
	gapsAgeRows(t, h, "products", product.ID)
	gapsAgeRows(t, h, "comments", comment.ID)
	h.reset(t)

	removed, err := PurgeExpiredTrash(h.db, time.Now())
	if err != nil {
		t.Fatalf("PurgeExpiredTrash: %v", err)
	}
	if removed != 3 {
		t.Fatalf("PurgeExpiredTrash removed %d rows, want 3", removed)
	}

	// Each purged row emits one purge delivery with a null actor (the purge is a system action).
	posts := pecExactly(t, h, plugin.HookOnPostDelete, 1)
	pecCheckBase(t, posts[0].Payload, "OnPostDelete")
	if posts[0].Payload["action"] != "purge" {
		t.Errorf("OnPostDelete action = %v, want purge", posts[0].Payload["action"])
	}
	pluginEventsShopWantNullActor(t, posts[0].Payload)
	summary := nested(t, posts[0].Payload, "post")
	if len(summary) != 4 {
		t.Errorf("purge post has %d keys, want exactly id, type, slug, title: %v", len(summary), summary)
	}
	for _, key := range []string{"id", "type", "slug", "title"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("purge post lacks %q: %v", key, summary)
		}
	}
	if number(t, summary, "id") != float64(postID) {
		t.Errorf("purge post id = %v, want %d", summary["id"], postID)
	}

	products := pluginEventsShopExactly(t, h, plugin.HookOnProductDelete, 1)
	if products[0].Payload["action"] != "purge" {
		t.Errorf("OnProductDelete action = %v, want purge", products[0].Payload["action"])
	}
	pluginEventsShopWantNullActor(t, products[0].Payload)
	if number(t, nested(t, products[0].Payload, "product"), "id") != float64(product.ID) {
		t.Errorf("purge product = %v, want id %d", products[0].Payload["product"], product.ID)
	}

	comments := pluginEventsShopExactly(t, h, plugin.HookOnCommentDelete, 1)
	pluginEventsShopWantNullActor(t, comments[0].Payload)
	purged := nested(t, comments[0].Payload, "comment")
	if number(t, purged, "id") != float64(comment.ID) || number(t, purged, "post_id") != float64(commentPost.ID) {
		t.Errorf("purge comment = %v, want id %d post_id %d", purged, comment.ID, commentPost.ID)
	}
}

func TestPluginEventsGapsWXRImport(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "gaps-import-admin")
	token := h.token(admin)
	h.reset(t)

	xml := impWXR(
		gapsWXRItem(101, "Gaps Published", "gaps-published", "publish"),
		gapsWXRItem(102, "Gaps Draft", "gaps-draft", "draft"),
	)
	upload := impUpload(h.accessHarness, token, "gaps-import.xml", []byte(xml), 0)
	if upload.code < 200 || upload.code > 299 {
		t.Fatalf("upload WXR = %d: %s", upload.code, upload.body)
	}
	jobID := impID(t, upload.body)
	run := impCall(h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/run", jobID), "", token)
	if run.code < 200 || run.code > 299 {
		t.Fatalf("run import = %d, want 2xx: %s", run.code, run.body)
	}

	saves := pecExactly(t, h, plugin.HookOnPostSave, 2)
	titles := map[string]bool{}
	for _, ev := range saves {
		pecCheckBase(t, ev.Payload, "OnPostSave")
		if ev.Payload["action"] != "import" {
			t.Errorf("OnPostSave action = %v, want import", ev.Payload["action"])
		}
		if _, ok := ev.Payload["actor"]; !ok {
			t.Errorf("OnPostSave import payload has no actor key: %v", ev.Payload)
		}
		titles[fmt.Sprint(nested(t, ev.Payload, "post")["title"])] = true
	}
	if !titles["Gaps Published"] || !titles["Gaps Draft"] {
		t.Errorf("import OnPostSave titles = %v, want Gaps Published and Gaps Draft", titles)
	}

	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	pecCheckBase(t, publishes[0].Payload, "OnPostPublish")
	if publishes[0].Payload["trigger"] != "save" {
		t.Errorf("OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	if title := nested(t, publishes[0].Payload, "post")["title"]; title != "Gaps Published" {
		t.Errorf("OnPostPublish post.title = %v, want Gaps Published (the publish item only)", title)
	}
}

func TestPluginEventsGapsOAuthFirstLogin(t *testing.T) {
	// Restore the process-wide Goth providers and session store after the test, as oauthTxnPrepareHarness does.
	previousProviders := cloneGothProviders(goth.GetProviders())
	previousStore := gothic.Store
	t.Cleanup(func() {
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, p := range previousProviders {
			providers = append(providers, p)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})

	h := newPluginEventsHarness(t)
	upstream := newOAuthHTTPUpstream(t, oauthHTTPBehavior{})
	goth.UseProviders(oauthHTTPProvider("google", upstream.server))
	h.reset(t)

	entry := oauthHTTPBegin(t, h.accessHarness, "google", "/admin")
	callback := oauthHTTPCallback(h.accessHarness, entry, "success-code", nil)
	if callback.Code != http.StatusOK {
		t.Fatalf("google callback returned %d, want 200: %s", callback.Code, callback.Body.String())
	}
	oauthHTTPAssertJSONLogin(t, callback)

	created := pecExactly(t, h, plugin.HookOnUserCreate, 1)
	pecCheckBase(t, created[0].Payload, "OnUserCreate")
	if created[0].Payload["source"] != "oauth" {
		t.Errorf("OnUserCreate source = %v, want oauth", created[0].Payload["source"])
	}
	user := nested(t, created[0].Payload, "user")
	if user["email"] != "oauth-http@example.test" {
		t.Errorf("OnUserCreate user.email = %v, want oauth-http@example.test", user["email"])
	}
	if _, ok := user["password"]; ok {
		t.Errorf("OnUserCreate user must not carry a password key: %v", user)
	}

	logins := pecExactly(t, h, plugin.HookOnUserLogin, 1)
	pecCheckBase(t, logins[0].Payload, "OnUserLogin")
	if logins[0].Payload["method"] != "oauth" {
		t.Errorf("OnUserLogin method = %v, want oauth", logins[0].Payload["method"])
	}
}

func TestPluginEventsGapsAutosaveIsNotPostSave(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "gaps-autosave-editor")
	token := h.token(editor)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Gaps autosave", "slug": "gaps-autosave", "content": "<p>seed</p>", "type": "post", "status": "draft"})
	pecExactly(t, h, plugin.HookOnPostSave, 1)
	h.reset(t)

	r := h.request(http.MethodPost, "/api/admin/posts/"+pecID(id)+"/autosave",
		pecJSON(t, map[string]any{"title": "Gaps autosave", "source": "<p>draft in progress</p>", "excerpt": ""}),
		token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "autosave")
	if r.code != http.StatusOK {
		t.Fatalf("autosave returned %d body=%q", r.code, r.body)
	}
	h.requireNoEvent(t, plugin.HookOnPostSave)
	h.requireNoEvent(t, plugin.HookOnPostPublish)
}
