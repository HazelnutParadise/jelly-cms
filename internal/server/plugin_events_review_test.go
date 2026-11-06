package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Review-found defect tests for the plugin event wiring
// (docs/tickets/28-plugin-events.md). Each test is written to fail on the
// current implementation where the ticket's contract is not met, and to pass
// once it is. Controls (expected to pass today) are labelled in comments.
// Helpers come from plugin_events_harness_test.go, plugin_events_content_test.go,
// plugin_events_misc_test.go and plugin_events_users_test.go.

// TestPluginEventsReviewPrivatePostIsNotPublic: a private post is not public, so
// neither a save nor a due sweep may announce it as published. Making it public
// afterwards must announce it once.
func TestPluginEventsReviewPrivatePostIsNotPublic(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "rev-private-admin")
	token := h.token(admin)
	h.reset(t)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Private", "slug": "rev-private", "content": "<p>p</p>", "type": "post", "status": "published", "visibility": "private"})
	pecExactly(t, h, plugin.HookOnPostSave, 1)
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	pecUpdatePost(t, h, token, id, map[string]any{"visibility": "public"})
	pecExactly(t, h, plugin.HookOnPostSave, 2)
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("visibility-public OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}

	// Scheduled path: a private future post must not be announced when due.
	pecPublishDue(t, h, time.Now(), 0)
	privateAt := time.Now().Add(2 * time.Second)
	pecCreatePost(t, h, token, map[string]any{"title": "Scheduled private", "slug": "rev-sched-private", "content": "<p>sp</p>", "type": "post", "status": "published", "visibility": "private", "published_at": privateAt})
	pecWaitPast(privateAt)
	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)

	// Control: a public future post is announced when due.
	publicAt := time.Now().Add(2 * time.Second)
	pecCreatePost(t, h, token, map[string]any{"title": "Scheduled public", "slug": "rev-sched-public", "content": "<p>sp</p>", "type": "post", "status": "published", "visibility": "public", "published_at": publicAt})
	pecWaitPast(publicAt)
	pecPublishDue(t, h, time.Now(), 1)
	pecExactly(t, h, plugin.HookOnPostPublish, 2)
}

// TestPluginEventsReviewWatermarkNeverMovesBack: a sweep with an earlier now must
// not rewind the publish watermark, otherwise posts already announced are
// announced again.
func TestPluginEventsReviewWatermarkNeverMovesBack(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "rev-wm-admin")
	token := h.token(admin)
	pecPublishDue(t, h, time.Now(), 0)
	h.reset(t)

	scheduledAt := time.Now().Add(2 * time.Second)
	pecCreatePost(t, h, token, map[string]any{"title": "Watermark", "slug": "rev-wm", "content": "<p>wm</p>", "type": "post", "status": "published", "published_at": scheduledAt})
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	pecWaitPast(scheduledAt)
	firstNow := time.Now()
	pecPublishDue(t, h, firstNow, 1)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)

	// An earlier now must find nothing new and must not move the watermark back.
	if got, err := publishDuePosts(context.Background(), h.db, firstNow.Add(-10*time.Minute)); err != nil {
		t.Fatalf("publishDuePosts with earlier now: %v", err)
	} else if got != 0 {
		t.Errorf("publishDuePosts with earlier now returned %d, want 0", got)
	}

	var row core.Option
	if err := h.db.Where("key = ?", publishWatermarkOption).Take(&row).Error; err != nil {
		t.Fatalf("read %s: %v", publishWatermarkOption, err)
	}
	stored, err := time.Parse(time.RFC3339Nano, row.Value)
	if err != nil {
		t.Fatalf("parse stored watermark %q: %v", row.Value, err)
	}
	if stored.Before(firstNow) {
		t.Errorf("watermark moved back to %s, earlier than first successful sweep %s", stored.Format(time.RFC3339Nano), firstNow.Format(time.RFC3339Nano))
	}

	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
}

// TestPluginEventsReviewRestoreTrashedDuePost: a scheduled post trashed before
// its time, then restored after its time, must be announced once by the restore
// (trigger save). A post restored after it was already announced must not be
// announced again.
func TestPluginEventsReviewRestoreTrashedDuePost(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "rev-restore-admin")
	token := h.token(admin)
	pecPublishDue(t, h, time.Now(), 0)
	h.reset(t)

	scheduledAt := time.Now().Add(2 * time.Second)
	id := pecCreatePost(t, h, token, map[string]any{"title": "Trashed due", "slug": "rev-trashed-due", "content": "<p>td</p>", "type": "post", "status": "published", "published_at": scheduledAt})
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(id), token)
	pecExactly(t, h, plugin.HookOnPostDelete, 1)
	h.reset(t)

	pecWaitPast(scheduledAt)
	pecAdminDo(t, h, http.MethodPost, "/api/admin/trash/post/"+pecID(id)+"/restore", token)
	saves := pecExactly(t, h, plugin.HookOnPostSave, 1)
	if saves[0].Payload["action"] != "restore" {
		t.Fatalf("restore OnPostSave action = %v, want restore", saves[0].Payload["action"])
	}
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("restore OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)

	// Control: restoring a post that was already live and announced adds no publish.
	h.reset(t)
	liveID := pecCreatePost(t, h, token, map[string]any{"title": "Already live", "slug": "rev-already-live", "content": "<p>al</p>", "type": "post", "status": "published"})
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(liveID), token)
	pecExactly(t, h, plugin.HookOnPostDelete, 1)
	pecAdminDo(t, h, http.MethodPost, "/api/admin/trash/post/"+pecID(liveID)+"/restore", token)
	pecExactly(t, h, plugin.HookOnPostSave, 2)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
}

// TestPluginEventsReviewRevisionRestoreAnnouncesDuePublish: a revision restore
// that makes a scheduled post live after its time is a save that moves the post
// public. It must announce OnPostPublish (trigger save), and the sweep must not
// repeat it.
func TestPluginEventsReviewRevisionRestoreAnnouncesDuePublish(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "rev-revision-admin")
	token := h.token(admin)
	pecPublishDue(t, h, time.Now(), 0)

	id := pecCreatePost(t, h, token, map[string]any{"title": "Versioned due", "slug": "rev-versioned-due", "content": "<p>first body</p>", "type": "post", "status": "draft"})
	pecUpdatePost(t, h, token, id, map[string]any{"content": "<p>second body</p>"})

	scheduledAt := time.Now().Add(2 * time.Second)
	pecUpdatePost(t, h, token, id, map[string]any{"status": "published", "published_at": scheduledAt})
	h.requireNoEvent(t, plugin.HookOnPostPublish)

	pecWaitPast(scheduledAt)
	h.reset(t)

	list := pecAdminDo(t, h, http.MethodGet, "/api/admin/posts/"+pecID(id)+"/revisions", token)
	var revs []struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(list.body), &revs); err != nil || len(revs) < 2 {
		t.Fatalf("expected at least two revisions, got %q (err %v)", list.body, err)
	}
	oldest := revs[0].ID
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
	publishes := pecExactly(t, h, plugin.HookOnPostPublish, 1)
	if publishes[0].Payload["trigger"] != "save" {
		t.Fatalf("revision restore OnPostPublish trigger = %v, want save", publishes[0].Payload["trigger"])
	}
	if pecPostID(t, publishes[0].Payload) != float64(id) {
		t.Fatalf("revision restore OnPostPublish post.id = %v, want %d", pecPostID(t, publishes[0].Payload), id)
	}

	pecPublishDue(t, h, time.Now(), 0)
	pecExactly(t, h, plugin.HookOnPostPublish, 1)
}

// TestPluginEventsReviewTrashPayloadCarriesCategories: OnPostDelete (trash) must
// carry the post's categories, like the other post payloads do.
func TestPluginEventsReviewTrashPayloadCarriesCategories(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "rev-cat-admin")
	token := h.token(admin)
	h.reset(t)

	r := h.request(http.MethodPost, "/api/admin/categories", pecJSON(t, map[string]any{"name": "Review News", "slug": "rev-news"}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create category")
	if r.code != http.StatusCreated {
		t.Fatalf("POST /api/admin/categories returned %d body=%q", r.code, r.body)
	}
	var cat struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.body), &cat); err != nil || cat.ID == 0 {
		t.Fatalf("category response has no id: %q (err %v)", r.body, err)
	}

	id := pecCreatePost(t, h, token, map[string]any{"title": "Categorised", "slug": "rev-categorised", "content": "<p>c</p>", "type": "post", "status": "draft", "category_ids": []uint{cat.ID}})
	h.reset(t)

	pecAdminDo(t, h, http.MethodDelete, "/api/admin/posts/"+pecID(id), token)
	deletes := pecExactly(t, h, plugin.HookOnPostDelete, 1)
	if deletes[0].Payload["action"] != "trash" {
		t.Fatalf("OnPostDelete action = %v, want trash", deletes[0].Payload["action"])
	}
	categories := pecArray(t, nested(t, deletes[0].Payload, "post"), "categories")
	if len(categories) != 1 {
		t.Fatalf("trash post categories has %d entries, want 1: %v", len(categories), categories)
	}
	first, ok := categories[0].(map[string]any)
	if !ok || first["slug"] != "rev-news" {
		t.Fatalf("trash post category = %v, want slug rev-news", categories[0])
	}
}

// TestPluginEventsReviewCommentApproveIsIdempotent: approving an already approved
// comment changes nothing, so it must not emit OnCommentUpdate. Approving a
// pending comment emits exactly one.
func TestPluginEventsReviewCommentApproveIsIdempotent(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "rev-cmt-editor")
	token := h.token(editor)
	post := commentsPublishedPost(t, h.accessHarness, editor, "rev-cmt-post", nil)
	approved := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "RevApproved", "approved", 1))
	pending := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "RevPending", "pending", 2))
	h.reset(t)

	r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/approve", approved.ID), "", token, "")
	if !commentsOK(t, r, "approve an approved comment", http.StatusOK) {
		return
	}
	h.requireNoEvent(t, plugin.HookOnCommentUpdate)

	r = commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/approve", pending.ID), "", token, "")
	if !commentsOK(t, r, "approve a pending comment", http.StatusOK) {
		return
	}
	update := peuExactly(t, h, plugin.HookOnCommentUpdate, 1)[0].Payload
	peuBase(t, update, "OnCommentUpdate")
	if number(t, nested(t, update, "comment"), "id") != float64(pending.ID) || update["from"] != "pending" || update["to"] != "approved" {
		t.Errorf("approve pending payload = %v, want comment %d pending to approved", update, pending.ID)
	}
}

// TestPluginEventsReviewCommentTrashSingleAndBulkMatch: a single trash and a bulk
// trash of pending comments must report the same shape: comment.status keeps the
// stored status ("pending") and to is "trash".
func TestPluginEventsReviewCommentTrashSingleAndBulkMatch(t *testing.T) {
	h := newPluginEventsHarness(t)
	editor := h.user("editor", "rev-trash-editor")
	token := h.token(editor)
	post := commentsPublishedPost(t, h.accessHarness, editor, "rev-trash-post", nil)
	single := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "RevTrashSingle", "pending", 3))
	bulk := commentsSeed(t, h.accessHarness, commentsRow(post.ID, "RevTrashBulk", "pending", 4))
	h.reset(t)

	if r := commentsDo(t, h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", single.ID), "", token, ""); !commentsOK(t, r, "single trash", http.StatusOK) {
		return
	}
	if r := commentsDo(t, h.accessHarness, http.MethodPost, "/api/admin/comments/bulk",
		commentsJSON(t, map[string]any{"ids": []uint{bulk.ID}, "action": "trash"}), token, ""); !commentsOK(t, r, "bulk trash", http.StatusOK) {
		return
	}

	delivered := peuExactly(t, h, plugin.HookOnCommentUpdate, 2)
	for _, tc := range []struct {
		name string
		id   uint
	}{{"single", single.ID}, {"bulk", bulk.ID}} {
		payload := pemCommentUpdateOf(t, delivered, tc.id)
		if payload["from"] != "pending" || payload["to"] != "trash" {
			t.Errorf("%s trash from/to = %v/%v, want pending/trash", tc.name, payload["from"], payload["to"])
		}
		if status := nested(t, payload, "comment")["status"]; status != "pending" {
			t.Errorf("%s trash comment.status = %v, want pending (stored status)", tc.name, status)
		}
	}
}

// TestPluginEventsReviewLoginMethodPasswordAfterOAuthAccount: an OAuth account
// that later gets a password logs in with method "password".
func TestPluginEventsReviewLoginMethodPasswordAfterOAuthAccount(t *testing.T) {
	const password = "rev-login-pass-1"
	h := newPluginEventsHarness(t)
	u := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "rev-oauth-user", Role: "author", Provider: "google"})

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := h.db.Model(&core.User{}).Where("id = ?", u.ID).Update("password", string(hash)).Error; err != nil {
		t.Fatalf("set password on OAuth account: %v", err)
	}
	h.reset(t)

	token, _ := acctLogin(t, h.accessHarness, u.Username, password, "", acctRemote())
	if token == "" {
		t.Fatal("password login returned no token")
	}
	login := peuExactly(t, h, plugin.HookOnUserLogin, 1)[0].Payload
	peuBase(t, login, "OnUserLogin")
	if login["method"] != "password" {
		t.Errorf("OnUserLogin method = %v, want password", login["method"])
	}
}

// TestPluginEventsReviewLoginMethodOAuth is not written: no exported function in
// internal/auth logs a user in with method "oauth" without a live goth provider
// round trip (HandleCallback needs the provider and its redirect). issueLogin,
// which decides the method, is unexported.
func TestPluginEventsReviewLoginMethodOAuth(t *testing.T) {
	t.Skip("no exported internal/auth entry point issues an OAuth login without a live provider callback; issueLogin (method oauth) is unexported")
}

// TestPluginEventsReviewPublicViewWritesTwiceEmitsOnce: a public page that
// writes its body in two Write calls must still report one OnPostView.
func TestPluginEventsReviewPublicViewWritesTwiceEmitsOnce(t *testing.T) {
	h := newPluginEventsHarness(t)
	pecUseActiveTheme(t, h)
	admin := h.user("admin", "rev-view-admin")
	token := h.token(admin)
	id := pecCreatePost(t, h, token, map[string]any{"title": "Multi write", "slug": "rev-multi-write", "content": "<p>mw</p>", "type": "post", "status": "published"})
	var post core.Post
	if err := h.db.First(&post, id).Error; err != nil {
		t.Fatalf("load post %d: %v", id, err)
	}

	h.echo.GET("/pec-multi-write", func(c echo.Context) error {
		stagePostViewEvent(c, post)
		if _, err := c.Response().Write([]byte("one")); err != nil {
			return err
		}
		_, err := c.Response().Write([]byte("two"))
		return err
	})
	h.reset(t)

	r := pecGet(h, http.MethodGet, "/pec-multi-write")
	h.mustNoPanic(r, "multi-write GET")
	if r.code != http.StatusOK {
		t.Fatalf("GET /pec-multi-write returned %d, want 200", r.code)
	}
	pecExactly(t, h, plugin.HookOnPostView, 1)
}

// TestPluginEventsReviewCreatePostSurvivesCanceledContext is not written as a
// defect proof: with the request context canceled before ServeHTTP, the admin
// auth layer answers 401 before the handler runs, so the write path (and the
// events.Transaction context) is never reached. Reproducing a disconnect while
// the handler is running needs a hook inside the handler, which the harness
// does not have.
func TestPluginEventsReviewCreatePostSurvivesCanceledContext(t *testing.T) {
	t.Skip("canceled request context is rejected with 401 by admin auth before the handler runs; a mid-handler disconnect cannot be reproduced through httptest")
}
