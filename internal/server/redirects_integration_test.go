package server

// Ticket 08b (docs/tickets/08-system.md): redirects (301, 302, 410), wildcard
// rules, target validation, loop and chain checks, expiry, hit counts, automatic
// redirects on slug change, CSV import and export, the test endpoint, the 404
// log and admin-only access. The contract is the 「轉址與 404」 bullet, the 錯誤與
// 邊界 section and the endpoint list. These tests fail until 08b lands. Do not edit
// them to match an implementation.
//
// Rule JSON uses the redirects table column names (from_path, to_url, kind,
// is_rule, keep_query, expires_at). CSV import is multipart with a "file" field.
// The test endpoint answers with a "chain" array. The 404 list answers with items
// carrying path, count, suggestion and id. These shapes are assumptions where the
// ticket does not name them.

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/labstack/echo/v4"
)

// s08bRuleCreate posts one redirect rule with the given admin token.
func s08bRuleCreate(t *testing.T, h *accessHarness, token string, rule map[string]any) accessResult {
	t.Helper()
	return h.request(http.MethodPost, "/api/admin/redirects", s08bJSON(t, rule), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

// s08bRuleMust creates a rule that must be accepted.
func s08bRuleMust(t *testing.T, h *accessHarness, token string, rule map[string]any) {
	t.Helper()
	r := s08bRuleCreate(t, h, token, rule)
	h.mustNoPanic(r, "POST /api/admin/redirects")
	s08bWant2xx(t, fmt.Sprintf("create redirect %v", rule), r)
}

// s08bRowByPath returns the list item whose path or from_path equals path.
func s08bRowByPath(items []map[string]any, path string) map[string]any {
	for _, item := range items {
		if item["path"] == path || item["from_path"] == path {
			return item
		}
	}
	return nil
}

func TestRedirectExactRuleKeepQueryAnd410(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-redirect-admin")
	token := h.token(admin)

	s08bRuleMust(t, h, token, map[string]any{"from_path": "/old-page", "to_url": "/new-page", "kind": 301})
	r := s08bGet(h, "/old-page", nil)
	s08bWant(t, "GET /old-page", r, http.StatusMovedPermanently)
	if got := r.header.Get("Location"); got != "/new-page" {
		t.Errorf("GET /old-page Location = %q, want /new-page", got)
	}

	// Without keep_query the query string is dropped.
	dropped := s08bGet(h, "/old-page?utm=1", nil)
	if got := dropped.header.Get("Location"); got != "/new-page" {
		t.Errorf("exact rule without keep_query Location = %q, want /new-page", got)
	}

	// With keep_query the query string moves to the target.
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/kq-page", "to_url": "/kq-target", "kind": 301, "keep_query": true})
	kept := s08bGet(h, "/kq-page?utm=1", nil)
	s08bWant(t, "GET /kq-page?utm=1", kept, http.StatusMovedPermanently)
	if got := kept.header.Get("Location"); got != "/kq-target?utm=1" {
		t.Errorf("keep_query Location = %q, want /kq-target?utm=1", got)
	}

	// Wildcard rule: only * is allowed, $1 carries the captured part.
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/old/*", "to_url": "/new/$1", "kind": 301, "is_rule": true, "keep_query": true})
	wild := s08bGet(h, "/old/abc?x=1", nil)
	s08bWant(t, "GET /old/abc?x=1", wild, http.StatusMovedPermanently)
	if got := wild.header.Get("Location"); got != "/new/abc?x=1" {
		t.Errorf("wildcard Location = %q, want /new/abc?x=1", got)
	}

	// 410 answers gone and has no target.
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/gone-page", "kind": 410})
	s08bWant(t, "GET /gone-page", s08bGet(h, "/gone-page", nil), http.StatusGone)
}

func TestRedirectTargetValidation(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-target-admin")
	token := h.token(admin)

	rejected := []string{
		"//evil.example/x",
		"/\\evil.example",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"/landing\r\nSet-Cookie: stolen=1",
		"https://user:secret@example.com/x",
		"$1/landing",
	}
	for i, to := range rejected {
		from := fmt.Sprintf("/val-bad-%d", i)
		r := s08bRuleCreate(t, h, token, map[string]any{"from_path": from, "to_url": to, "kind": 301})
		h.mustNoPanic(r, "POST redirect target "+to)
		if r.code != http.StatusBadRequest {
			t.Errorf("target %q accepted with status %d, want 400", to, r.code)
		}
		if got := s08bGet(h, from, nil); got.code == http.StatusMovedPermanently {
			t.Errorf("rejected target %q is served as a 301", to)
		}
	}

	accepted := []string{"/landing-ok", "https://example.com/landing"}
	for i, to := range accepted {
		r := s08bRuleCreate(t, h, token, map[string]any{"from_path": fmt.Sprintf("/val-ok-%d", i), "to_url": to, "kind": 301})
		h.mustNoPanic(r, "POST redirect target "+to)
		s08bWant2xx(t, "admin target "+to, r)
	}
}

func TestRedirectLoopAndChainDepth(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-loop-admin")
	token := h.token(admin)

	s08bRuleMust(t, h, token, map[string]any{"from_path": "/loop-a", "to_url": "/loop-b", "kind": 301})
	r := s08bRuleCreate(t, h, token, map[string]any{"from_path": "/loop-b", "to_url": "/loop-a", "kind": 301})
	h.mustNoPanic(r, "POST loop rule")
	if r.code != http.StatusBadRequest {
		t.Errorf("loop /loop-b -> /loop-a accepted with status %d, want 400", r.code)
	}

	// Five hops are allowed.
	for i := 0; i < 5; i++ {
		s08bRuleMust(t, h, token, map[string]any{"from_path": fmt.Sprintf("/chain-%d", i), "to_url": fmt.Sprintf("/chain-%d", i+1), "kind": 301})
	}
	// The sixth hop is refused when the rule is saved.
	deep := s08bRuleCreate(t, h, token, map[string]any{"from_path": "/chain-5", "to_url": "/chain-6", "kind": 301})
	h.mustNoPanic(deep, "POST sixth hop")
	if deep.code != http.StatusBadRequest {
		t.Errorf("chain deeper than 5 accepted with status %d, want 400", deep.code)
	}
}

func TestRedirectExpiryAndHits(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-expiry-admin")
	token := h.token(admin)

	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/expiry-page", "to_url": "/expiry-target", "kind": 301, "expires_at": expires})
	s08bWant(t, "GET /expiry-page before expiry", s08bGet(h, "/expiry-page", nil), http.StatusMovedPermanently)

	// Time passes: the rule's expiry is now in the past.
	if err := h.db.Exec(`UPDATE redirects SET expires_at = now() - interval '1 minute' WHERE from_path = ?`, "/expiry-page").Error; err != nil {
		t.Fatalf("expire rule: %v", err)
	}
	if got := s08bGet(h, "/expiry-page", nil); got.code == http.StatusMovedPermanently {
		t.Errorf("expired rule is still applied (301)")
	}

	s08bRuleMust(t, h, token, map[string]any{"from_path": "/hits-page", "to_url": "/hits-target", "kind": 301})
	for i := 0; i < 3; i++ {
		s08bWant(t, "GET /hits-page", s08bGet(h, "/hits-page", nil), http.StatusMovedPermanently)
	}
	if err := redirects.FlushHits(); err != nil {
		t.Fatalf("flush redirect hits job: %v", err)
	}
	if hits := s08bCount(t, h, `SELECT hits FROM redirects WHERE from_path = ?`, "/hits-page"); hits != 3 {
		t.Errorf("hits after flush = %d, want 3", hits)
	}
}

func TestRedirectAutoOnSlugChange(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-auto-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-auto-author")
	post := s08bSeedPost(t, h, author, s08bPost{title: "Auto Redirect Probe", slug: "auto-old-slug"})

	r := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", post.ID), `{"slug":"auto-new-slug"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "PUT post slug")
	s08bWant(t, "PUT post slug", r, http.StatusOK)

	old := s08bGet(h, "/post/auto-old-slug", nil)
	s08bWant(t, "GET /post/auto-old-slug", old, http.StatusMovedPermanently)
	if got := old.header.Get("Location"); got != "/post/auto-new-slug" {
		t.Errorf("auto redirect Location = %q, want /post/auto-new-slug", got)
	}
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM redirects WHERE source = 'auto' AND kind = 301`); n < 1 {
		t.Errorf("slug change created %d auto 301 rules, want at least 1", n)
	}
}

func TestRedirectCSVImportDryRunAndExport(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-csv-admin")
	jwt := h.token(admin)

	csvBody := "from_path,to_url,kind\n/csv-a,/csv-target-a,301\n/csv-b,/csv-target-b,302\n"
	ctype, payload := accessMultipartBody(t, "file", "redirects.csv", csvBody)

	dry := s08bInteractive(h, http.MethodPost, "/api/admin/redirects/import?dry=1", bytes.NewReader(payload), jwt, ctype)
	h.mustNoPanic(dry, "POST redirects import dry run")
	s08bWant(t, "POST /api/admin/redirects/import?dry=1", dry, http.StatusOK)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM redirects WHERE from_path LIKE '/csv-%'`); n != 0 {
		t.Errorf("dry run wrote %d rows, want 0", n)
	}

	imported := s08bInteractive(h, http.MethodPost, "/api/admin/redirects/import", bytes.NewReader(payload), jwt, ctype)
	h.mustNoPanic(imported, "POST redirects import")
	s08bWant(t, "POST /api/admin/redirects/import", imported, http.StatusOK)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM redirects WHERE from_path LIKE '/csv-%'`); n != 2 {
		t.Errorf("import wrote %d rows, want 2", n)
	}

	// A from_path that starts with = is an export formula; the exporter must prefix it.
	if err := h.db.Exec(`INSERT INTO redirects (from_path, to_url, kind, source) VALUES ('=cmd-probe', '/export-target', 301, 'manual')`).Error; err != nil {
		t.Fatalf("seed formula row: %v", err)
	}
	exp := h.request(http.MethodGet, "/api/admin/redirects/export.csv", "", jwt, "text/csv", "")
	h.mustNoPanic(exp, "GET export.csv")
	s08bWant(t, "GET /api/admin/redirects/export.csv", exp, http.StatusOK)
	if !strings.Contains(exp.body, "'=cmd-probe") {
		t.Errorf("export does not escape the formula cell as '=cmd-probe; body=%q", s08bSnippet(exp.body))
	}
	if strings.Contains(exp.body, ",=cmd-probe") || strings.Contains(exp.body, "\n=cmd-probe") {
		t.Errorf("export writes the formula cell unescaped")
	}
}

func TestRedirectTestEndpointShowsChain(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-chain-admin")
	token := h.token(admin)
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/tst-a", "to_url": "/tst-b", "kind": 301})
	s08bRuleMust(t, h, token, map[string]any{"from_path": "/tst-b", "to_url": "/tst-c", "kind": 302})

	r := h.request(http.MethodPost, "/api/admin/redirects/test", `{"path":"/tst-a"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "POST /api/admin/redirects/test")
	s08bWant(t, "POST /api/admin/redirects/test", r, http.StatusOK)
	decoded := s08bDecode(t, r.body)
	if _, ok := s08bFindKey(decoded, "chain"); !ok {
		t.Errorf("test endpoint response has no chain; body=%q", s08bSnippet(r.body))
	}
	archivesWantText(t, "POST /api/admin/redirects/test", r.body, "/tst-b", "/tst-c")
}

func TestNotFoundLogAggregatesCapsAndIgnores(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-404-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-404-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Summer Sale Entry", slug: "summer-sale"})

	// The same missing path twice aggregates into one row with count 2.
	s08bWant(t, "first miss", s08bGet(h, "/nf-twice", nil), http.StatusNotFound)
	s08bWant(t, "second miss", s08bGet(h, "/nf-twice", nil), http.StatusNotFound)
	list := s08bItems(t, h.request(http.MethodGet, "/api/admin/not-found", "", token, echo.MIMEApplicationJSON, "").body)
	row := s08bRowByPath(list, "/nf-twice")
	if row == nil {
		t.Errorf("GET /api/admin/not-found has no row for /nf-twice")
		row = map[string]any{}
	}
	if n, ok := s08bNumber(row["count"]); !ok || n != 2 {
		t.Errorf("/nf-twice count = %v, want 2", row["count"])
	}
	nfTwiceID, _ := s08bNumber(row["id"])

	// The query string is never stored; the path is stored without it.
	s08bWant(t, "miss with query", s08bGet(h, "/nf-query?utm=1&x=2", nil), http.StatusNotFound)
	list = s08bItems(t, h.request(http.MethodGet, "/api/admin/not-found", "", token, echo.MIMEApplicationJSON, "").body)
	if s08bRowByPath(list, "/nf-query") == nil {
		t.Errorf("miss with a query string was not stored under /nf-query")
	}
	for _, item := range list {
		if p, _ := item["path"].(string); strings.Contains(p, "?") {
			t.Errorf("stored path %q contains a query string", p)
		}
	}

	// Paths over 512 bytes are not stored.
	s08bGet(h, "/"+strings.Repeat("l", 600), nil)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log WHERE char_length(path) > 512`); n != 0 {
		t.Errorf("%d stored paths are longer than 512 bytes, want 0", n)
	}

	// A near miss of an existing slug carries a suggestion.
	s08bWant(t, "miss near an existing slug", s08bGet(h, "/summer-sal", nil), http.StatusNotFound)
	list = s08bItems(t, h.request(http.MethodGet, "/api/admin/not-found", "", token, echo.MIMEApplicationJSON, "").body)
	near := s08bRowByPath(list, "/summer-sal")
	if near == nil {
		t.Errorf("GET /api/admin/not-found has no row for /summer-sal")
		near = map[string]any{}
	}
	if suggestion, _ := near["suggestion"].(string); !strings.Contains(suggestion, "summer-sale") {
		t.Errorf("suggestion for /summer-sal = %q, want a link to summer-sale", suggestion)
	}

	ignore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/not-found/%d/ignore", int64(nfTwiceID)), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(ignore, "POST not-found ignore")
	s08bWant(t, "POST /api/admin/not-found/:id/ignore", ignore, http.StatusOK, http.StatusNoContent)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log WHERE path = ? AND ignored = TRUE`, "/nf-twice"); n != 1 {
		t.Errorf("ignored rows for /nf-twice = %d, want 1", n)
	}

	// The table keeps at most 5000 distinct paths; the 5001st evicts the oldest.
	if err := h.db.Exec(`INSERT INTO not_found_log (path, path_hash, count, first_seen, last_seen)
		SELECT '/seed-' || g, md5('/seed-' || g), 1, now() - ((5000 - g) * interval '1 second'), now() - ((5000 - g) * interval '1 second')
		FROM generate_series(1, 5000) AS g`).Error; err != nil {
		t.Fatalf("seed 5000 not-found rows: %v", err)
	}
	s08bWant(t, "miss beyond the cap", s08bGet(h, "/nf-cap-probe", nil), http.StatusNotFound)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log`); n > 5000 {
		t.Errorf("not_found_log holds %d rows, want at most 5000", n)
	}
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log WHERE path = ?`, "/seed-1"); n != 0 {
		t.Errorf("oldest path /seed-1 was not evicted")
	}
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log WHERE path = ?`, "/nf-cap-probe"); n != 1 {
		t.Errorf("newest path /nf-cap-probe is not stored")
	}

	cleared := h.request(http.MethodDelete, "/api/admin/not-found", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(cleared, "DELETE not-found")
	s08bWant(t, "DELETE /api/admin/not-found", cleared, http.StatusOK, http.StatusNoContent)
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM not_found_log`); n != 0 {
		t.Errorf("clear left %d rows, want 0", n)
	}
}

func TestRedirectsAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "s08b-redirect-editor")
	author := h.user("author", "s08b-redirect-author")
	admin := h.user("admin", "s08b-redirect-only-admin")
	editorToken, authorToken, adminToken := h.token(editor), h.token(author), h.token(admin)

	type call struct{ method, path, body string }
	calls := []call{
		{http.MethodGet, "/api/admin/redirects", ""},
		{http.MethodPost, "/api/admin/redirects", `{"from_path":"/x","to_url":"/y","kind":301}`},
		{http.MethodPost, "/api/admin/redirects/test", `{"path":"/x"}`},
		{http.MethodGet, "/api/admin/redirects/export.csv", ""},
		{http.MethodGet, "/api/admin/not-found", ""},
		{http.MethodDelete, "/api/admin/not-found", ""},
	}
	for _, c := range calls {
		for name, token := range map[string]string{"editor": editorToken, "author": authorToken} {
			r := h.request(c.method, c.path, c.body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, c.method+" "+c.path)
			if r.code != http.StatusForbidden {
				t.Errorf("%s %s as %s returned %d, want 403", c.method, c.path, name, r.code)
			}
		}
	}
	// Positive control: the admin reads the rule list.
	r := h.request(http.MethodGet, "/api/admin/redirects", "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "GET /api/admin/redirects as admin")
	s08bWant(t, "GET /api/admin/redirects as admin", r, http.StatusOK)
}
