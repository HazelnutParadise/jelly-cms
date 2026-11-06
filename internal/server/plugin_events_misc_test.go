package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/events"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Comment, media, theme, settings, outbox and plugin log event tests
// (docs/tickets/27-plugin-events.md, 留言／附件／主題／設定／交易 rows of the
// acceptance table and the log tab). Helpers shared with the users file
// (peuExactly, peuBase, peuWantAdmin, peuStrings, peuSettle) come from
// plugin_events_users_test.go.

// pemCommentID decodes the id of a comment-shaped response body.
func pemCommentID(t *testing.T, r accessResult, where string) uint {
	t.Helper()
	var out struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.body), &out); err != nil || out.ID == 0 {
		t.Fatalf("%s: no comment id in response; body=%q err=%v", where, r.body, err)
	}
	return out.ID
}

// pemCommentUpdateOf returns the OnCommentUpdate payload for comment id.
func pemCommentUpdateOf(t *testing.T, delivered []pluginEvent, id uint) map[string]any {
	t.Helper()
	for _, ev := range delivered {
		if number(t, nested(t, ev.Payload, "comment"), "id") == float64(id) {
			return ev.Payload
		}
	}
	t.Fatalf("no OnCommentUpdate for comment %d among %+v", id, delivered)
	return nil
}

func TestPluginEventsMiscPublicCommentCreate(t *testing.T) {
	h := newPluginEventsHarness(t)
	author := h.user("author", "pem-comment-author")
	post := commentsPublishedPost(t, h.accessHarness, author, "pem-public-post", nil)

	r := commentsDo(t, h.accessHarness, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": "pem public body"}), "", "")
	if !commentsOK(t, r, "guest POST /api/comments", http.StatusCreated) {
		return
	}
	id := pemCommentID(t, r, "guest POST /api/comments")

	payload := peuExactly(t, h, plugin.HookOnCommentCreate, 1)[0].Payload
	peuBase(t, payload, "OnCommentCreate")
	comment := nested(t, payload, "comment")
	if number(t, comment, "id") != float64(id) {
		t.Errorf("comment.id = %v, want %d", comment["id"], id)
	}
	if number(t, comment, "post_id") != float64(post.ID) {
		t.Errorf("comment.post_id = %v, want %d", comment["post_id"], post.ID)
	}
	if comment["name"] != "Guest" || comment["body"] != "pem public body" {
		t.Errorf("comment name/body = %v/%v, want Guest/pem public body", comment["name"], comment["body"])
	}
	if row, ok := commentsLoad(t, h.db, id); ok && comment["status"] != row.Status {
		t.Errorf("comment.status = %v, database says %q", comment["status"], row.Status)
	}
	for _, banned := range []string{"ip", "user_agent", "email"} {
		if _, has := comment[banned]; has {
			t.Errorf("comment carries forbidden key %q: %v", banned, comment)
		}
	}
	postObj := nested(t, payload, "post")
	if number(t, postObj, "id") != float64(post.ID) || postObj["slug"] != "pem-public-post" || postObj["title"] != post.Title {
		t.Errorf("post = %v, want id %d, slug pem-public-post, title %q", postObj, post.ID, post.Title)
	}
	if payload["actor"] != nil {
		t.Errorf("anonymous comment actor = %v, want null", payload["actor"])
	}
}

func TestPluginEventsMiscModerateApproveTrashRestore(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "pem-mod-editor")
	post := commentsPublishedPost(t, h.accessHarness, editor, "pem-mod-post", nil)
	token := h.token(editor)
	c := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemMod", "pending", 1))

	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/approve", c.ID), "", token, ""); !commentsOK(t, r, "approve", http.StatusOK) {
		return
	}
	approved := peuExactly(t, h, plugin.HookOnCommentUpdate, 1)[0].Payload
	peuBase(t, approved, "OnCommentUpdate")
	if number(t, nested(t, approved, "comment"), "id") != float64(c.ID) || approved["from"] != "pending" || approved["to"] != "approved" {
		t.Errorf("approve payload comment/from/to = %v/%v/%v, want %d pending approved", approved["comment"], approved["from"], approved["to"], c.ID)
	}
	peuWantAdmin(t, approved, editor.ID)

	h.reset(t)
	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", c.ID), "", token, ""); !commentsOK(t, r, "trash", http.StatusOK) {
		return
	}
	trashed := peuExactly(t, h, plugin.HookOnCommentUpdate, 1)[0].Payload
	if trashed["to"] != "trash" || number(t, nested(t, trashed, "comment"), "id") != float64(c.ID) {
		t.Errorf("trash payload to/comment = %v/%v, want trash and %d", trashed["to"], trashed["comment"], c.ID)
	}
	peuWantAdmin(t, trashed, editor.ID)

	h.reset(t)
	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/restore", c.ID), "", token, ""); !commentsOK(t, r, "restore", http.StatusOK) {
		return
	}
	restored := peuExactly(t, h, plugin.HookOnCommentUpdate, 1)[0].Payload
	if restored["from"] != "trash" {
		t.Errorf("restore from = %v, want trash", restored["from"])
	}
	// The ticket names "restore" as the restore target; a payload that reports
	// the status the row returned to is accepted as well.
	if row, ok := commentsLoad(t, h.db, c.ID); ok && restored["to"] != "restore" && restored["to"] != row.Status {
		t.Errorf("restore to = %v, want restore or the restored status %q", restored["to"], row.Status)
	}
}

func TestPluginEventsMiscBulkApproveEmitsOnePerComment(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "pem-bulk-editor")
	post := commentsPublishedPost(t, h.accessHarness, editor, "pem-bulk-post", nil)
	token := h.token(editor)
	p1 := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemBulk1", "pending", 1))
	p2 := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemBulk2", "pending", 2))
	p3 := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemBulk3", "approved", 3))

	r := commentsDo(t, h.accessHarness, http.MethodPost, "/api/admin/comments/bulk",
		commentsJSON(t, map[string]any{"ids": []uint{p1.ID, p2.ID}, "action": "approve"}), token, "")
	if !commentsOK(t, r, "bulk approve", http.StatusOK) {
		return
	}
	seen := map[float64]bool{}
	for _, ev := range peuExactly(t, h, plugin.HookOnCommentUpdate, 2) {
		peuBase(t, ev.Payload, "OnCommentUpdate")
		seen[number(t, nested(t, ev.Payload, "comment"), "id")] = true
		if ev.Payload["to"] != "approved" {
			t.Errorf("bulk OnCommentUpdate to = %v, want approved", ev.Payload["to"])
		}
		peuWantAdmin(t, ev.Payload, editor.ID)
	}
	if !seen[float64(p1.ID)] || !seen[float64(p2.ID)] || seen[float64(p3.ID)] {
		t.Errorf("bulk OnCommentUpdate comment ids = %v, want %d and %d only", seen, p1.ID, p2.ID)
	}
}

func TestPluginEventsMiscPermanentDeleteEmitsOnCommentDelete(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "pem-del-editor")
	post := commentsPublishedPost(t, h.accessHarness, editor, "pem-del-post", nil)
	token := h.token(editor)
	c := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemDel", "pending", 1))

	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", c.ID), "", token, ""); !commentsOK(t, r, "trash before purge", http.StatusOK) {
		return
	}
	h.reset(t)
	if r := commentsDo(t, h.accessHarness, http.MethodDelete, fmt.Sprintf("/api/admin/comments/%d", c.ID), "", token, ""); !commentsOK(t, r, "permanent delete", http.StatusOK) {
		return
	}
	payload := peuExactly(t, h, plugin.HookOnCommentDelete, 1)[0].Payload
	peuBase(t, payload, "OnCommentDelete")
	comment := nested(t, payload, "comment")
	if number(t, comment, "id") != float64(c.ID) || number(t, comment, "post_id") != float64(post.ID) {
		t.Errorf("OnCommentDelete comment = %v, want id %d post_id %d", comment, c.ID, post.ID)
	}
	peuWantAdmin(t, payload, editor.ID)
}

func TestPluginEventsMiscReplyCreatesAndApprovesParent(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "pem-reply-editor")
	author := h.user("author", "pem-reply-author")
	post := commentsPublishedPost(t, h.accessHarness, author, "pem-reply-post", nil)
	parent := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "PemParent", "pending", 1))

	r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/reply", parent.ID),
		commentsJSON(t, map[string]any{"body": "pem reply body"}), h.token(editor), "")
	if !commentsOK(t, r, "editor reply", http.StatusCreated) {
		return
	}
	replyID := pemCommentID(t, r, "editor reply")

	created := peuExactly(t, h, plugin.HookOnCommentCreate, 1)[0].Payload
	peuBase(t, created, "OnCommentCreate")
	comment := nested(t, created, "comment")
	if number(t, comment, "id") != float64(replyID) {
		t.Errorf("reply comment.id = %v, want %d", comment["id"], replyID)
	}
	if number(t, comment, "parent_id") != float64(parent.ID) {
		t.Errorf("reply comment.parent_id = %v, want %d", comment["parent_id"], parent.ID)
	}
	if number(t, nested(t, created, "post"), "id") != float64(post.ID) {
		t.Errorf("reply post.id = %v, want %d", nested(t, created, "post")["id"], post.ID)
	}
	peuWantAdmin(t, created, editor.ID)

	// A pending parent is approved by the reply, which emits its own update.
	h.waitEvents(t, plugin.HookOnCommentUpdate, 1)
	update := pemCommentUpdateOf(t, h.events(t, pluginEventsFixtureID, plugin.HookOnCommentUpdate), parent.ID)
	if update["from"] != "pending" || update["to"] != "approved" {
		t.Errorf("parent OnCommentUpdate from/to = %v/%v, want pending/approved", update["from"], update["to"])
	}
}

// pemUpload posts one file through the admin media upload route on policyID.
func pemUpload(t *testing.T, h *pluginEventsHarness, token string, policyID int64, name string, data []byte) accessResult {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("policy_id", strconv.FormatInt(policyID, 10)); err != nil {
		t.Fatalf("write policy_id field: %v", err)
	}
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return acctCallReader(t, h.accessHarness, acctRemote(), http.MethodPost, "/api/admin/media/files", &buf, token,
		map[string]string{echo.HeaderContentType: mw.FormDataContentType()})
}

func TestPluginEventsMiscMediaUploadAndTrash(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-media-admin")
	token := h.token(admin)
	policy := medPolicy(t, h.accessHarness, token, "Events uploads")

	up := pemUpload(t, h, token, policy, "pem-pixel.png", acctTinyPNG(t))
	h.mustNoPanic(up, "media upload")
	if !acctWant(t, up, "POST /api/admin/media/files", http.StatusCreated) {
		return
	}
	var created map[string]any
	acctDecode(t, up, "media upload response", &created)
	fileID := number(t, created, "id")

	payload := peuExactly(t, h, plugin.HookOnMediaUpload, 1)[0].Payload
	peuBase(t, payload, "OnMediaUpload")
	file := nested(t, payload, "file")
	if number(t, file, "id") != fileID || file["path"] != created["path"] || file["filename"] != created["filename"] || file["mime"] != "image/png" {
		t.Errorf("OnMediaUpload file = %v, want id %v path %v filename %v mime image/png", file, created["id"], created["path"], created["filename"])
	}
	if number(t, file, "size") <= 0 {
		t.Errorf("OnMediaUpload file.size = %v, want > 0", file["size"])
	}
	if payload["source"] != "upload" {
		t.Errorf("OnMediaUpload source = %v, want upload", payload["source"])
	}
	peuWantAdmin(t, payload, admin.ID)

	h.reset(t)
	del := acctJSON(t, h.accessHarness, http.MethodDelete, fmt.Sprintf("/api/admin/media/files/%d", int64(fileID)), "", token)
	h.mustNoPanic(del, "media trash")
	if !acctWantOK(t, del, "DELETE /api/admin/media/files/:id") {
		return
	}
	trashed := peuExactly(t, h, plugin.HookOnMediaDelete, 1)[0].Payload
	peuBase(t, trashed, "OnMediaDelete")
	file = nested(t, trashed, "file")
	if number(t, file, "id") != fileID || file["path"] != created["path"] || file["filename"] != created["filename"] || file["mime"] != "image/png" {
		t.Errorf("OnMediaDelete file = %v, want id %v path %v filename %v mime image/png", file, created["id"], created["path"], created["filename"])
	}
	peuWantAdmin(t, trashed, admin.ID)
}

func TestPluginEventsMiscThemeActivate(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-theme-admin")
	token := h.token(admin)

	r := acctJSON(t, h.accessHarness, http.MethodPost, "/api/admin/themes/activate", `{"name":"default"}`, token)
	h.mustNoPanic(r, "theme activate")
	if !acctWant(t, r, "POST /api/admin/themes/activate default", http.StatusOK) {
		return
	}
	payload := peuExactly(t, h, plugin.HookOnThemeActivate, 1)[0].Payload
	peuBase(t, payload, "OnThemeActivate")
	if payload["theme"] != "default" {
		t.Errorf("OnThemeActivate theme = %v, want default", payload["theme"])
	}
	if _, ok := payload["previous"]; !ok {
		t.Errorf("OnThemeActivate has no previous key: %v", payload)
	}
	peuWantAdmin(t, payload, admin.ID)

	h.reset(t)
	missing := acctJSON(t, h.accessHarness, http.MethodPost, "/api/admin/themes/activate", `{"name":"pem-no-such-theme"}`, token)
	h.mustNoPanic(missing, "activate a missing theme")
	if missing.code >= 200 && missing.code < 300 {
		t.Fatalf("activating a missing theme returned %d, want an error; body=%q", missing.code, missing.body)
	}
	h.requireNoEvent(t, plugin.HookOnThemeActivate)
}

func TestPluginEventsMiscSettingsChangeCarriesKeysNotValues(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-settings-admin")
	const key = "site_title"
	if spec, ok := settings.Lookup(key); !ok || spec.Tab == "internal" || spec.Kind != settings.KindString {
		t.Fatalf("setting %s must be a registered, non-internal string key", key)
	}

	const value = "pem settings value never in event"
	if err := settings.Set(context.Background(), admin.ID, map[string]string{key: value}); err != nil {
		t.Fatalf("settings.Set: %v", err)
	}
	payload := peuExactly(t, h, plugin.HookOnSettingsChange, 1)[0].Payload
	peuBase(t, payload, "OnSettingsChange")
	keys := peuStrings(t, payload, "keys")
	if len(keys) != 1 || keys[0] != key {
		t.Errorf("OnSettingsChange keys = %v, want [%s]", keys, key)
	}
	if strings.Contains(fmt.Sprint(payload), value) {
		t.Errorf("OnSettingsChange payload carries the setting value: %v", payload)
	}
	peuWantAdmin(t, payload, admin.ID)
}

func TestPluginEventsMiscTransactionOutbox(t *testing.T) {
	h := newPluginEventsHarness(t)

	err := events.Transaction(context.Background(), h.db, func(tx *gorm.DB) error {
		stagePluginEvent(tx.Statement.Context, plugin.HookOnSettingsChange, map[string]any{"keys": []string{"x"}})
		return nil
	})
	if err != nil {
		t.Fatalf("committed transaction returned %v", err)
	}
	peuExactly(t, h, plugin.HookOnSettingsChange, 1)

	h.reset(t)
	boom := errors.New("pem boom")
	err = events.Transaction(context.Background(), h.db, func(tx *gorm.DB) error {
		stagePluginEvent(tx.Statement.Context, plugin.HookOnSettingsChange, map[string]any{"keys": []string{"x"}})
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("failed transaction returned %v, want the callback error", err)
	}
	h.requireNoEvent(t, plugin.HookOnSettingsChange)
}

func TestPluginEventsMiscStageOutsideTransactionIsDropped(t *testing.T) {
	h := newPluginEventsHarness(t)
	publishPluginEvent(plugin.HookOnSettingsChange, map[string]any{"keys": []string{"explicit"}})
	peuExactly(t, h, plugin.HookOnSettingsChange, 1)

	h.reset(t)
	stagePluginEvent(context.Background(), plugin.HookOnSettingsChange, map[string]any{"keys": []string{"staged"}})
	h.requireNoEvent(t, plugin.HookOnSettingsChange)
}

// pemInstallScriptPlugin writes and loads a plugin whose entry is script. The
// enabled plugins row carries the granted hash, so the event filter decides on
// the manifest alone.
func pemInstallScriptPlugin(t *testing.T, h *pluginEventsHarness, id, script string, withEvents bool) {
	t.Helper()
	dir := filepath.Join("data", "plugins", id)
	meta := plugin.PluginMetadata{ID: id, Name: "Misc " + id, Version: "1.0.0", Entrypoint: "index.js"}
	if withEvents {
		meta.Permissions = []plugin.PermissionSpec{{Name: "events"}}
	}
	writeReloadPlugin(t, dir, meta, script)
	granted := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash) VALUES (?, 'plugin', '1.0.0', 'upload', TRUE, 'unsigned', ?) ON CONFLICT (id) DO UPDATE SET enabled = TRUE, permissions_granted_hash = EXCLUDED.permissions_granted_hash`, id, granted).Error; err != nil {
		t.Fatalf("insert plugins row for %s: %v", id, err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load plugin %s: %v", id, err)
	}
}

type pemLogRow struct {
	Level   int    `json:"level"`
	Message string `json:"message"`
}

// pemLogs reads GET /api/admin/plugins/:id/logs as the admin.
func pemLogs(t *testing.T, h *pluginEventsHarness, token, id string) []pemLogRow {
	t.Helper()
	r := h.request(http.MethodGet, "/api/admin/plugins/"+id+"/logs", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "plugin logs "+id)
	if r.code != http.StatusOK {
		t.Fatalf("GET plugin logs %s returned %d body=%q", id, r.code, r.body)
	}
	var rows []pemLogRow
	if err := json.Unmarshal([]byte(r.body), &rows); err != nil {
		t.Fatalf("decode plugin logs %q: %v", r.body, err)
	}
	return rows
}

// pemWaitLogs polls the logs tab until ready reports true, failing after five seconds.
func pemWaitLogs(t *testing.T, h *pluginEventsHarness, token, id string, ready func([]pemLogRow) bool) []pemLogRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows := pemLogs(t, h, token, id)
		if ready(rows) {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin %s logs never satisfied the expectation; rows=%+v\ncaptured log:\n%s", id, rows, h.sink.dump())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPluginEventsMiscLogsTabShowsWarnAndHookError(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-log-admin")
	token := h.token(admin)

	pemInstallScriptPlugin(t, h, "pem-logger", `JellyCMS.log.warn("misc-warn");`, false)
	pemWaitLogs(t, h, token, "pem-logger", func(rows []pemLogRow) bool {
		for _, row := range rows {
			if row.Message == "misc-warn" && row.Level == syslog.LevelWarn {
				return true
			}
		}
		return false
	})

	pemInstallScriptPlugin(t, h, "pem-thrower", `JellyCMS.registerHook("OnSettingsChange", function (data) { throw new Error("pem boom"); });`, true)
	if err := settings.Set(context.Background(), admin.ID, map[string]string{"site_title": "pem throws"}); err != nil {
		t.Fatalf("settings.Set: %v", err)
	}
	h.flush(t)
	pemWaitLogs(t, h, token, "pem-thrower", func(rows []pemLogRow) bool {
		for _, row := range rows {
			if row.Level == syslog.LevelError && strings.Contains(row.Message, "OnSettingsChange") {
				return true
			}
		}
		return false
	})
}

func TestPluginEventsMiscSuspendsAfterFiveConsecutiveFailures(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-suspend-admin")
	token := h.token(admin)
	const id = "pem-thrower-seven"
	pemInstallScriptPlugin(t, h, id, `JellyCMS.registerHook("OnSettingsChange", function (data) { throw new Error("pem boom"); });`, true)

	for i := 1; i <= 7; i++ {
		if err := settings.Set(context.Background(), admin.ID, map[string]string{"site_title": fmt.Sprintf("pem-%d", i)}); err != nil {
			t.Fatalf("settings.Set %d: %v", i, err)
		}
	}
	// The healthy fixture plugin still receives all seven events.
	peuExactly(t, h, plugin.HookOnSettingsChange, 7)

	pemWaitLogs(t, h, token, id, func(rows []pemLogRow) bool {
		for _, row := range rows {
			if row.Level == syslog.LevelWarn && strings.Contains(row.Message, "events suspended") {
				return true
			}
		}
		return false
	})
	time.Sleep(peuSettle)
	rows := pemLogs(t, h, token, id)
	errs, suspended := 0, 0
	for _, row := range rows {
		if row.Level == syslog.LevelError {
			errs++
		}
		if row.Level == syslog.LevelWarn && strings.Contains(row.Message, "events suspended") {
			suspended++
		}
	}
	if errs != 5 {
		t.Errorf("error rows for the failing plugin = %d, want exactly 5 (suspension after five failures); rows=%+v", errs, rows)
	}
	if suspended != 1 {
		t.Errorf("'events suspended' warn rows = %d, want exactly 1; rows=%+v", suspended, rows)
	}
}

func TestPluginEventsMiscHookOrderMatchesCallOrder(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "pem-order-admin")

	if err := settings.Set(context.Background(), admin.ID, map[string]string{"site_title": "first"}); err != nil {
		t.Fatalf("first settings.Set: %v", err)
	}
	if err := settings.Set(context.Background(), admin.ID, map[string]string{"site_description": "second"}); err != nil {
		t.Fatalf("second settings.Set: %v", err)
	}
	got := peuExactly(t, h, plugin.HookOnSettingsChange, 2)
	first := peuStrings(t, got[0].Payload, "keys")
	second := peuStrings(t, got[1].Payload, "keys")
	if len(first) != 1 || first[0] != "site_title" || len(second) != 1 || second[0] != "site_description" {
		t.Errorf("OnSettingsChange order = %v then %v, want [site_title] then [site_description]", first, second)
	}
}
