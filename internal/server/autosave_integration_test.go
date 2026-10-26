package server

// Ticket 09 autosave contract (docs/tickets/09-editor.md, 流程與驗收 and API table):
//
//	POST /api/admin/posts/:id/autosave {title, source, excerpt}   owner or editor; writes an autosave revision only
//	GET  /api/admin/posts/:id                                    returns autosave when it is newer than updated_at
//	PUT  /api/admin/posts/:id  with base_updated_at               409 when the base does not match
//
// Autosave never touches posts.content or posts.updated_at. At most core.RevisionAutosaveKeep (20)
// autosaves are kept per post. Runs through the real route table on the dedicated PostgreSQL database;
// skipped unless JELLY_TEST_POSTGRES=1. Written before the endpoint exists; do not edit them to match
// an implementation.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func edtAutosave(h *accessHarness, token string, id uint, title, source string) accessResult {
	raw := edtBody(h.t, map[string]any{"title": title, "source": source, "excerpt": ""})
	return h.request(http.MethodPost, "/api/admin/posts/"+edtID(id)+"/autosave", raw, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

func edtCountAutosaves(t *testing.T, h *accessHarness, id uint) int {
	t.Helper()
	var n int64
	if err := h.db.Raw("SELECT COUNT(*) FROM revisions WHERE post_id = ? AND kind = 'autosave'", id).Scan(&n).Error; err != nil {
		t.Fatalf("count autosave revisions: %v", err)
	}
	return int(n)
}

func TestAutosaveWritesRevisionOnlyAndTrimsToTwenty(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-autosave-author")
	token := h.token(author)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "自動儲存", "source": "原稿 0", "content": "<p>seed</p>"})
	before := edtLoadRow(t, h, id)
	if n := edtCountAutosaves(t, h, id); n != 0 {
		t.Fatalf("fixture has %d autosaves before the test, want 0", n)
	}

	for i := 1; i <= 25; i++ {
		r := edtAutosave(h, token, id, "自動儲存", fmt.Sprintf("原稿 %d", i))
		h.mustNoPanic(r, "autosave")
		if r.code != http.StatusOK {
			t.Fatalf("autosave %d returned %d body=%q", i, r.code, r.body)
		}
	}

	after := edtLoadRow(t, h, id)
	if after.Content != before.Content {
		t.Errorf("autosave changed posts.content: %q → %q", before.Content, after.Content)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("autosave changed posts.updated_at: %s → %s", before.UpdatedAt, after.UpdatedAt)
	}
	if n := edtCountAutosaves(t, h, id); n != 20 {
		t.Errorf("autosave revisions kept = %d, want 20", n)
	}

	var newest string
	if err := h.db.Raw("SELECT COALESCE(source, '') FROM revisions WHERE post_id = ? AND kind = 'autosave' ORDER BY id DESC LIMIT 1", id).Scan(&newest).Error; err != nil {
		t.Fatalf("read newest autosave: %v", err)
	}
	if newest != "原稿 25" {
		t.Errorf("newest autosave source = %q, want 原稿 25", newest)
	}
}

func TestAutosaveOwnershipAndRoles(t *testing.T) {
	h := newAccessHarness(t)
	owner := h.user("author", "edt-own-owner")
	other := h.user("author", "edt-own-other")
	editor := h.user("editor", "edt-own-editor")
	customer := h.user("customer", "edt-own-customer")

	id, _ := edtCreate(t, h, h.token(owner), map[string]any{"title": "擁有權", "source": "x"})

	if r := edtAutosave(h, h.token(other), id, "擁有權", "他人"); r.code != http.StatusNotFound {
		t.Errorf("another author's autosave = %d, want 404 (body %q)", r.code, r.body)
	}
	if n := edtCountAutosaves(t, h, id); n != 0 {
		t.Errorf("a refused autosave wrote %d revisions, want 0", n)
	}

	if r := edtAutosave(h, h.token(owner), id, "擁有權", "自己"); r.code != http.StatusOK {
		t.Errorf("owner autosave = %d, want 200 (body %q)", r.code, r.body)
	}
	if r := edtAutosave(h, h.token(editor), id, "擁有權", "編輯"); r.code != http.StatusOK {
		t.Errorf("editor autosave = %d, want 200 (body %q)", r.code, r.body)
	}
	if r := edtAutosave(h, h.token(customer), id, "擁有權", "客人"); r.code != http.StatusForbidden {
		t.Errorf("customer autosave = %d, want 403 (body %q)", r.code, r.body)
	}
}

func TestPostGetReturnsNewerAutosave(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-newer-author")
	token := h.token(author)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "較新草稿", "source": "舊原稿"})

	first := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(first, "get post")
	if first.code != http.StatusOK {
		t.Fatalf("GET post returned %d body=%q", first.code, first.body)
	}
	if strings.Contains(first.body, `"autosave":{`) {
		t.Errorf("GET post must not return an autosave before one is written: %s", first.body)
	}

	time.Sleep(10 * time.Millisecond)
	if r := edtAutosave(h, token, id, "較新草稿", "較新的原稿"); r.code != http.StatusOK {
		t.Fatalf("autosave returned %d body=%q", r.code, r.body)
	}

	newer := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(newer, "get post after autosave")
	got := edtDecode(t, newer.body)
	autosave, ok := got["autosave"].(map[string]any)
	if !ok {
		t.Fatalf("GET post must return autosave when it is newer than updated_at: %s", newer.body)
	}
	if autosave["source"] != "較新的原稿" {
		t.Errorf("autosave source = %v, want 較新的原稿", autosave["source"])
	}

	// A later real save makes the post newer than the autosave, so the autosave is no longer returned.
	time.Sleep(10 * time.Millisecond)
	edtMustUpdate(t, h, token, id, map[string]any{"title": "正式儲存"})
	saved := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(saved, "get post after save")
	if strings.Contains(saved.body, `"autosave":{`) {
		t.Errorf("GET post must drop an autosave older than updated_at: %s", saved.body)
	}
}

func TestPostUpdateBaseUpdatedAtConflict(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-conflict-author")
	token := h.token(author)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "衝突", "source": "原稿"})
	current := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(current, "get post")
	updatedAt, _ := edtDecode(t, current.body)["updated_at"].(string)
	if updatedAt == "" {
		t.Fatalf("GET post carries no updated_at: %s", current.body)
	}

	stale := edtUpdate(h, token, id, map[string]any{"title": "過期", "base_updated_at": "2001-01-01T00:00:00Z"})
	h.mustNoPanic(stale, "stale update")
	if stale.code != http.StatusConflict {
		t.Errorf("stale base_updated_at = %d, want 409 (body %q)", stale.code, stale.body)
	}

	fresh := edtUpdate(h, token, id, map[string]any{"title": "新", "base_updated_at": updatedAt})
	h.mustNoPanic(fresh, "fresh update")
	if fresh.code != http.StatusOK {
		t.Errorf("matching base_updated_at = %d, want 200 (body %q)", fresh.code, fresh.body)
	}

	again := edtUpdate(h, token, id, map[string]any{"title": "又一次", "base_updated_at": updatedAt})
	h.mustNoPanic(again, "repeat update")
	if again.code != http.StatusConflict {
		t.Errorf("base_updated_at that the first save superseded = %d, want 409", again.code)
	}
}
