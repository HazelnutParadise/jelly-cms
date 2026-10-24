package server

// Ticket 09 render API contract (docs/tickets/09-editor.md, API table, 錯誤與邊界):
//
//	POST /api/admin/render {source, post_id?, mode: fragment|page, sourcemap, expand}
//	GET  /api/admin/render/schema
//
// Roles: admin, editor and author (the content group). Anonymous 401, other roles 403. The render never
// writes. Source is limited to 2 MB (413) and each user to 60 renders a minute (429). Runs through the
// real route table against the dedicated PostgreSQL database; skipped unless JELLY_TEST_POSTGRES=1.
// Written before the endpoint exists and expected to fail until the editor routes are built.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

const edtRenderPath = "/api/admin/render"

func edtRenderBody(t *testing.T, source string) string {
	t.Helper()
	return edtBody(t, map[string]any{"source": source, "mode": "fragment", "sourcemap": false, "expand": false})
}

func TestRenderEndpointRoleGate(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-render-author")
	customer := h.user("customer", "edt-render-customer")
	body := edtRenderBody(t, "# 測試")

	anon := h.request(http.MethodPost, edtRenderPath, body, "", echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(anon, "anonymous render")
	if anon.code != http.StatusUnauthorized {
		t.Errorf("anonymous render = %d, want 401 (body %q)", anon.code, anon.body)
	}

	denied := h.request(http.MethodPost, edtRenderPath, body, h.token(customer), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(denied, "customer render")
	if denied.code != http.StatusForbidden {
		t.Errorf("customer render = %d, want 403 (body %q)", denied.code, denied.body)
	}

	allowed := h.request(http.MethodPost, edtRenderPath, body, h.token(author), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(allowed, "author render")
	if allowed.code != http.StatusOK {
		t.Errorf("author render = %d, want 200 (body %q)", allowed.code, allowed.body)
	}
}

func TestRenderEndpointShapeAndNoWrites(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-shape-author")
	token := h.token(author)

	source := "## 標題\n\n- [ ] 待辦\n\n腳註[^1]\n\n[^1]: 內容\n\n" +
		":::columns\n::: col\n左\n:::\n::: col\n右\n:::\n:::\n\n$x$\n\n![圖](/uploads/a.png)\n"

	before := accessStateSnapshot(t, h.db)
	var revsBefore, postsBefore int64
	h.db.Raw("SELECT COUNT(*) FROM revisions").Scan(&revsBefore)
	h.db.Raw("SELECT COUNT(*) FROM posts").Scan(&postsBefore)

	r := h.request(http.MethodPost, edtRenderPath, edtRenderBody(t, source), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "render")
	if r.code != http.StatusOK {
		t.Fatalf("render returned %d body=%q", r.code, r.body)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.body), &got); err != nil {
		t.Fatalf("render response is not a JSON object: %v body=%q", err, r.body)
	}
	for _, key := range []string{"html", "toc", "stats", "features", "diagnostics", "blocks", "renderer"} {
		if _, ok := got[key]; !ok {
			t.Errorf("render response is missing %q: %s", key, r.body)
		}
	}
	var html string
	_ = json.Unmarshal(got["html"], &html)
	if strings.TrimSpace(html) == "" {
		t.Errorf("render html is empty")
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(got["blocks"], &blocks); err != nil || len(blocks) == 0 {
		t.Errorf("render blocks must be a non-empty list for a document with :::columns: %s", got["blocks"])
	}

	if after := accessStateSnapshot(t, h.db); after != before {
		t.Errorf("render must not change the database or isolated files")
	}
	var revsAfter, postsAfter int64
	h.db.Raw("SELECT COUNT(*) FROM revisions").Scan(&revsAfter)
	h.db.Raw("SELECT COUNT(*) FROM posts").Scan(&postsAfter)
	if revsAfter != revsBefore || postsAfter != postsBefore {
		t.Errorf("render wrote rows: revisions %d→%d, posts %d→%d", revsBefore, revsAfter, postsBefore, postsAfter)
	}
}

func TestRenderEndpointLimits(t *testing.T) {
	t.Run("source over 2 MB is 413", func(t *testing.T) {
		h := newAccessHarness(t)
		author := h.user("author", "edt-limit-author")
		source := strings.Repeat("a", 2*1024*1024+1)
		r := h.request(http.MethodPost, edtRenderPath, edtRenderBody(t, source), h.token(author), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "oversized render")
		if r.code != http.StatusRequestEntityTooLarge {
			t.Errorf("2 MB + 1 source = %d, want 413 (body %q)", r.code, r.body)
		}
	})

	t.Run("61 rapid renders are rate limited", func(t *testing.T) {
		h := newAccessHarness(t)
		author := h.user("author", "edt-rate-author")
		token := h.token(author)
		body := edtRenderBody(t, "# 限流")

		limited := 0
		var last int
		for i := 1; i <= 61; i++ {
			r := h.request(http.MethodPost, edtRenderPath, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, "rapid render")
			last = r.code
			if r.code == http.StatusTooManyRequests {
				limited++
			} else if r.code >= 500 {
				t.Fatalf("render %d returned %d body=%q", i, r.code, r.body)
			}
		}
		if limited == 0 || last != http.StatusTooManyRequests {
			t.Errorf("61 rapid renders: %d were 429, last status %d; want the 61st to be 429", limited, last)
		}
	})
}

func TestRenderSchemaListsBlocksAndSlashItems(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-schema-author")

	r := h.request(http.MethodGet, "/api/admin/render/schema", "", h.token(author), echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "render schema")
	if r.code != http.StatusOK {
		t.Fatalf("GET render schema returned %d body=%q", r.code, r.body)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatalf("schema is not a JSON object: %v body=%q", err, r.body)
	}

	for _, name := range []string{"hero", "section", "columns", "sidebar", "cta", "gallery", "faq", "tabs", "products", "posts", "embed", "widget", "counter", "compare", "countdown"} {
		if !strings.Contains(r.body, `"`+name+`"`) {
			t.Errorf("schema must list the %q block", name)
		}
	}
	if !strings.Contains(r.body, `"mint"`) {
		t.Errorf("schema must list the bg enumeration including mint")
	}

	slashFound := false
	for key, value := range doc {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "slash") || strings.Contains(lower, "command") || strings.Contains(lower, "menu") {
			var items []json.RawMessage
			if json.Unmarshal(value, &items) == nil && len(items) > 0 {
				slashFound = true
			}
		}
	}
	if !slashFound {
		t.Errorf("schema must carry a non-empty slash-menu list: %s", r.body)
	}
}
