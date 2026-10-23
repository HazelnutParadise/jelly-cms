package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

func TestPostInputRejectsNonSingleJSONWithoutChangingState(t *testing.T) {
	badBodies := []struct {
		name string
		body string
	}{
		{"second object", `{"title":"first"}{"title":"second"}`},
		{"trailing junk", `{"title":"first"} trailing`},
		{"null", `null`},
		{"array", `[]`},
	}
	roles := []string{"admin", "author"}
	methods := []string{http.MethodPost, http.MethodPut}
	for _, method := range methods {
		for _, role := range roles {
			for _, bad := range badBodies {
				t.Run(method+"/"+role+"/"+bad.name, func(t *testing.T) {
					h := newAccessHarness(t)
					user := h.user(role, "post-input-"+role)
					token := h.token(user)
					path := "/api/admin/posts"
					if method == http.MethodPut {
						post := core.Post{Title: "Existing", Slug: "existing-" + role, Content: "keep", Type: "post", Status: "draft", AuthorID: user.ID, Meta: json.RawMessage(`{"keep":true}`)}
						if err := h.db.Create(&post).Error; err != nil {
							t.Fatalf("create update fixture: %v", err)
						}
						path = fmt.Sprintf("%s/%d", path, post.ID)
					}
					before := accessStateSnapshot(t, h.db)
					response := h.request(method, path, bad.body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
					h.mustNoPanic(response, method+" "+path)
					if response.code != http.StatusBadRequest {
						t.Errorf("%s %s with %s returned %d body=%q, want 400", method, path, bad.name, response.code, response.body)
					}
					if after := accessStateSnapshot(t, h.db); after != before {
						t.Errorf("%s %s with %s changed database or files", method, path, bad.name)
					}
				})
			}
		}
	}
}

func TestPostInputValidJSONAndPartialUpdatePersistFields(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "post-input-author")
	admin := h.user("admin", "post-input-admin")
	published := time.Date(2026, 10, 22, 4, 5, 6, 0, time.UTC)
	create := h.request(http.MethodPost, "/api/admin/posts", `{"title":"Author post","content":"original","status":"published","meta":{"kind":"object"},"published_at":"2026-10-22T04:05:06Z"}`+" \n\t", h.token(author), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if create.code != http.StatusOK {
		t.Fatalf("author POST with trailing whitespace returned %d body=%q", create.code, create.body)
	}
	var authorPost core.Post
	if err := json.Unmarshal([]byte(create.body), &authorPost); err != nil {
		t.Fatalf("decode author POST response: %v", err)
	}
	if authorPost.Meta == nil || !jsonEqual(authorPost.Meta, `{"kind":"object"}`) || authorPost.PublishedAt == nil || !authorPost.PublishedAt.Equal(published) {
		t.Fatalf("author POST metadata not persisted: meta=%s published_at=%v", authorPost.Meta, authorPost.PublishedAt)
	}

	adminCreate := h.request(http.MethodPost, "/api/admin/posts", `{"title":"Admin post","content":"admin","meta":[1,"two",{"three":3}],"published_at":null}`, h.token(admin), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if adminCreate.code != http.StatusOK {
		t.Fatalf("admin POST returned %d body=%q", adminCreate.code, adminCreate.body)
	}
	var adminPost core.Post
	if err := json.Unmarshal([]byte(adminCreate.body), &adminPost); err != nil {
		t.Fatalf("decode admin POST response: %v", err)
	}
	if !jsonEqual(adminPost.Meta, `[1,"two",{"three":3}]`) || adminPost.PublishedAt != nil {
		t.Fatalf("admin POST metadata not persisted: meta=%s published_at=%v", adminPost.Meta, adminPost.PublishedAt)
	}

	var stored core.Post
	if err := h.db.First(&stored, authorPost.ID).Error; err != nil {
		t.Fatalf("reload author post: %v", err)
	}
	updated := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", stored.ID), `{"title":"Updated title"}`+" \n\t", h.token(author), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if updated.code != http.StatusOK {
		t.Fatalf("author partial PUT with trailing whitespace returned %d body=%q", updated.code, updated.body)
	}
	if err := h.db.First(&stored, authorPost.ID).Error; err != nil {
		t.Fatalf("reload partially updated post: %v", err)
	}
	if stored.Title != "Updated title" || stored.Content != "original" || stored.Status != "published" || !jsonEqual(stored.Meta, `{"kind":"object"}`) || stored.PublishedAt == nil || !stored.PublishedAt.Equal(published) {
		t.Errorf("partial PUT did not preserve omitted fields: %+v", stored)
	}

	clear := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", adminPost.ID), `{"meta":null,"published_at":null}`, h.token(admin), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if clear.code != http.StatusOK {
		t.Fatalf("admin PUT clearing nullable fields returned %d body=%q", clear.code, clear.body)
	}
	stored = core.Post{}
	if err := h.db.First(&stored, adminPost.ID).Error; err != nil {
		t.Fatalf("reload post after nullable update: %v", err)
	}
	if string(stored.Meta) != "null" || stored.PublishedAt != nil {
		t.Errorf("explicit null update was not persisted: meta=%s published_at=%v", stored.Meta, stored.PublishedAt)
	}
}

func TestPostInputContentTypeObservation(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "post-input-content-type")
	response := h.request(http.MethodPost, "/api/admin/posts", `{"title":"Content type probe"}`, h.token(admin), echo.MIMEApplicationJSON, "text/plain")
	t.Logf("POST /api/admin/posts with Content-Type text/plain returned %d", response.code)
}

func jsonEqual(left json.RawMessage, right string) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// TestPostsPublishSetsPublishedAt checks that publishing stamps published_at when the body omits it,
// that drafts keep it null, that later edits do not bump it, and that an explicit value is kept.
func TestPostsPublishSetsPublishedAt(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "publish-at-author")
	token := h.token(author)
	const window = 5 * time.Second

	decodePost := func(where string, r accessResult) core.Post {
		t.Helper()
		if r.panicked != nil {
			t.Fatalf("%s panicked: %v", where, r.panicked)
		}
		if r.code != http.StatusOK {
			t.Fatalf("%s returned %d body=%q", where, r.code, r.body)
		}
		var p core.Post
		if err := json.Unmarshal([]byte(r.body), &p); err != nil {
			t.Fatalf("%s: decode response: %v; body=%q", where, err, r.body)
		}
		return p
	}
	assertNow := func(where string, p core.Post, before, after time.Time) {
		t.Helper()
		if p.PublishedAt == nil {
			t.Fatalf("%s: published_at is null, want a timestamp near now", where)
		}
		if p.PublishedAt.Before(before.Add(-window)) || p.PublishedAt.After(after.Add(window)) {
			t.Errorf("%s: published_at = %s, want within %s of now (%s..%s)", where, p.PublishedAt, window, before, after)
		}
	}

	before := time.Now()
	published := decodePost("POST published without published_at", h.request(http.MethodPost, "/api/admin/posts",
		`{"title":"Stamp on create","content":"body","status":"published"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON))
	assertNow("POST published without published_at", published, before, time.Now())

	draft := decodePost("POST draft", h.request(http.MethodPost, "/api/admin/posts",
		`{"title":"Stamp draft","content":"body","status":"draft"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON))
	if draft.PublishedAt != nil {
		t.Errorf("POST draft published_at = %s, want null", draft.PublishedAt)
	}

	path := fmt.Sprintf("/api/admin/posts/%d", draft.ID)
	before = time.Now()
	promoted := decodePost("PUT draft to published", h.request(http.MethodPut, path,
		`{"status":"published"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON))
	assertNow("PUT draft to published", promoted, before, time.Now())
	if promoted.PublishedAt == nil {
		t.Fatalf("PUT draft to published left published_at null")
	}
	stamp := *promoted.PublishedAt

	renamed := decodePost("PUT new title", h.request(http.MethodPut, path,
		`{"title":"Stamp renamed"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON))
	if renamed.PublishedAt == nil || !renamed.PublishedAt.Equal(stamp) {
		t.Errorf("PUT new title changed published_at from %s to %v; want it unchanged", stamp, renamed.PublishedAt)
	}

	explicit := "2026-10-22T04:05:06Z"
	want, err := time.Parse(time.RFC3339, explicit)
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	kept := decodePost("POST with explicit published_at", h.request(http.MethodPost, "/api/admin/posts",
		`{"title":"Stamp explicit","content":"body","status":"published","published_at":"`+explicit+`"}`,
		token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON))
	if kept.PublishedAt == nil || !kept.PublishedAt.Equal(want) {
		t.Errorf("POST with explicit published_at = %v, want %s", kept.PublishedAt, want)
	}
}
