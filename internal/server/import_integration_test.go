package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// impWXR wraps items in a WXR 1.2 document with two authors (alice, bob).
func impWXR(items ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
<title>Fixture</title>
<link>https://old.example.test</link>
<wp:base_site_url>https://old.example.test</wp:base_site_url>
<wp:author><wp:author_id>1</wp:author_id><wp:author_login>alice</wp:author_login><wp:author_email>alice@old.example.test</wp:author_email><wp:author_display_name>Alice</wp:author_display_name></wp:author>
<wp:author><wp:author_id>2</wp:author_id><wp:author_login>bob</wp:author_login><wp:author_email>bob@old.example.test</wp:author_email><wp:author_display_name>Bob</wp:author_display_name></wp:author>
` + strings.Join(items, "\n") + `
</channel>
</rss>`
}

func impPost(id int, title, slug, author, content string, comments ...string) string {
	return fmt.Sprintf(`<item>
<title>%s</title>
<link>https://old.example.test/2026/01/%s/</link>
<dc:creator><![CDATA[%s]]></dc:creator>
<content:encoded><![CDATA[%s]]></content:encoded>
<wp:post_id>%d</wp:post_id>
<wp:post_name>%s</wp:post_name>
<wp:post_type>post</wp:post_type>
<wp:status>publish</wp:status>
%s
</item>`, title, slug, author, content, id, slug, strings.Join(comments, "\n"))
}

func impComment(id int, author, approved string) string {
	return fmt.Sprintf(`<wp:comment><wp:comment_id>%d</wp:comment_id><wp:comment_author>%s</wp:comment_author><wp:comment_author_email>%s@example.test</wp:comment_author_email><wp:comment_content><![CDATA[Nice post]]></wp:comment_content><wp:comment_approved>%s</wp:comment_approved><wp:comment_type>comment</wp:comment_type></wp:comment>`, id, author, author, approved)
}

func impPage(id int, title, slug string) string {
	return fmt.Sprintf(`<item><title>%s</title><link>https://old.example.test/%s/</link><dc:creator><![CDATA[alice]]></dc:creator><content:encoded><![CDATA[<p>About us</p>]]></content:encoded><wp:post_id>%d</wp:post_id><wp:post_name>%s</wp:post_name><wp:post_type>page</wp:post_type><wp:status>publish</wp:status></item>`, title, slug, id, slug)
}

func impAttachment(id int, url string) string {
	return fmt.Sprintf(`<item><title>photo</title><link>%s</link><dc:creator><![CDATA[alice]]></dc:creator><wp:post_id>%d</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:attachment_url>%s</wp:attachment_url></item>`, url, id, url)
}

// impUpload posts a WXR file as multipart/form-data. declaredLength, when
// non-zero, overrides the Content-Length the server sees.
func impUpload(h *accessHarness, token, name string, data []byte, declaredLength int64) accessResult {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import/wxr", &buf)
	req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
	req.Header.Set("X-Jelly-CSRF", "1")
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	if declaredLength > 0 {
		req.ContentLength = declaredLength
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

// impCall sends an authenticated JSON request with the admin session cookie.
func impCall(h *accessHarness, method, path, body, token string) accessResult {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Jelly-CSRF", "1")
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

// impFindKey returns the first value stored under key anywhere in a decoded
// JSON tree.
func impFindKey(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val, true
		}
		for _, child := range x {
			if found, ok := impFindKey(child, key); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range x {
			if found, ok := impFindKey(child, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func impJSONNumber(t *testing.T, body, key string) float64 {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	val, ok := impFindKey(v, key)
	num, isNum := val.(float64)
	if !ok || !isNum {
		t.Fatalf("response has no numeric %q: %s", key, body)
	}
	return num
}

func impID(t *testing.T, body string) int64 {
	return int64(impJSONNumber(t, body, "id"))
}

func impCount(t *testing.T, h *accessHarness, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func TestImportWxrUploadSummarizesAndMapsAuthors(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-sum-admin")
	token := h.token(admin)
	xml := impWXR(
		impPost(10, "Hello World", "hello-world", "alice", "<p>Hi</p>", impComment(1, "eve", "1")),
		impPost(11, "Second", "second", "bob", "<p>Two</p>"),
		impPage(20, "About", "about"),
		impAttachment(30, "https://old.example.test/wp-content/uploads/photo.jpg"),
	)
	res := impUpload(h, token, "export.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("POST /api/admin/import/wxr = %d, want 2xx: %s", res.code, res.body)
	}
	for key, want := range map[string]float64{"posts": 2, "pages": 1, "attachments": 1, "comments": 1, "authors": 2} {
		if got := impJSONNumber(t, res.body, key); got != want {
			t.Errorf("summary %s = %v, want %v", key, got, want)
		}
	}
	for _, login := range []string{"alice", "bob"} {
		if !strings.Contains(res.body, login) {
			t.Errorf("authors list does not include %q: %s", login, res.body)
		}
	}
}

func TestImportRejectsNonWxrAndOversize(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-reject-admin")
	token := h.token(admin)

	for name, body := range map[string]string{
		"html.xml":   "<html><body>not a WordPress export</body></html>",
		"random.xml": "<root><item>nothing here</item></root>",
	} {
		if res := impUpload(h, token, name, []byte(body), 0); res.code != http.StatusBadRequest {
			t.Errorf("upload %s = %d, want 400", name, res.code)
		}
	}

	big := []byte(impWXR())
	if res := impUpload(h, token, "huge.xml", big, 512<<20+1); res.code != http.StatusRequestEntityTooLarge {
		t.Errorf("declared 512 MiB+1 upload = %d, want 413", res.code)
	}
}

func TestImportDryRunReportsConflicts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-dry-admin")
	token := h.token(admin)
	if err := h.db.Create(&core.Post{Title: "Existing", Slug: "hello-world", Content: "local", Type: "post", Status: "published", AuthorID: admin.ID}).Error; err != nil {
		t.Fatal(err)
	}
	// dave is an author on the item but missing from the author list.
	xml := impWXR(impPost(10, "Hello World", "hello-world", "dave", "<p>Hi</p>"))
	res := impUpload(h, token, "conflict.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)

	dry := impCall(h, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/dry-run", id), "", token)
	if dry.code != http.StatusOK {
		t.Fatalf("dry-run = %d, want 200: %s", dry.code, dry.body)
	}
	var v any
	if err := json.Unmarshal([]byte(dry.body), &v); err != nil {
		t.Fatalf("dry-run body is not JSON: %v", err)
	}
	conflicts, ok := impFindKey(v, "conflicts")
	list, isList := conflicts.([]any)
	if !ok || !isList || len(list) == 0 {
		t.Fatalf("dry-run reports no conflicts: %s", dry.body)
	}
	if !strings.Contains(dry.body, "hello-world") {
		t.Error("slug collision for hello-world not reported")
	}
	if !strings.Contains(dry.body, "dave") {
		t.Error("missing author dave not reported")
	}
}

func TestImportRunIsIdempotentAndRewritesURLs(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-run-admin")
	token := h.token(admin)
	xml := impWXR(
		impPost(10, "Hello World", "hello-world", "alice", `<p>Read <a href="https://old.example.test/about/">about</a></p>`),
		impPage(20, "About", "about"),
	)
	res := impUpload(h, token, "run.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)
	runPath := fmt.Sprintf("/api/admin/import/%d/run", id)

	for i := 1; i <= 2; i++ {
		if r := impCall(h, http.MethodPost, runPath, "", token); r.code < 200 || r.code > 299 {
			t.Fatalf("run %d = %d, want 2xx: %s", i, r.code, r.body)
		}
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM posts WHERE slug = 'hello-world'"); n != 1 {
		t.Errorf("hello-world rows after two runs = %d, want 1", n)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM posts WHERE slug = 'about' AND type = 'page'"); n != 1 {
		t.Errorf("about page rows after two runs = %d, want 1", n)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM import_map WHERE job_id = ?", id); n != 3 {
		t.Errorf("import_map rows = %d, want 3 (2 posts + 1 page, each once)", n)
	}

	var content string
	h.db.Raw("SELECT content FROM posts WHERE slug = 'hello-world'").Scan(&content)
	if strings.Contains(content, "old.example.test") {
		t.Errorf("content still links to the old domain: %s", content)
	}

	if n := impCount(t, h, "SELECT COUNT(*) FROM redirects WHERE source = 'import' AND from_path = '/2026/01/hello-world/'"); n != 1 {
		t.Errorf("import redirect for the old permalink = %d rows, want 1", n)
	}
	old := httptest.NewRecorder()
	h.echo.ServeHTTP(old, httptest.NewRequest(http.MethodGet, "/2026/01/hello-world/", nil))
	if old.Code != http.StatusMovedPermanently {
		t.Errorf("old permalink GET = %d, want 301", old.Code)
	}
}

func TestImportSkipsSpamCommentsAndSanitizes(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-spam-admin")
	token := h.token(admin)
	xml := impWXR(impPost(10, "Unsafe", "unsafe", "alice",
		`<p>Safe text</p><script>alert(1)</script>`,
		impComment(1, "realreader", "1"),
		impComment(2, "spambot", "spam"),
	))
	res := impUpload(h, token, "spam.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)
	if r := impCall(h, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/run", id), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("run = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM comments"); n != 1 {
		t.Errorf("imported comments = %d, want 1 (spam skipped)", n)
	}
	var content string
	h.db.Raw("SELECT content FROM posts WHERE slug = 'unsafe'").Scan(&content)
	if strings.Contains(strings.ToLower(content), "<script") {
		t.Errorf("imported content kept a script element: %s", content)
	}
	if !strings.Contains(content, "Safe text") {
		t.Errorf("sanitizing removed safe text: %s", content)
	}
}

func TestImportAuthorsBecomeInvitedUsers(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-auth-admin")
	token := h.token(admin)
	xml := impWXR(impPost(10, "By Alice", "by-alice", "alice", "<p>x</p>"))
	res := impUpload(h, token, "authors.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)
	if r := impCall(h, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/run", id), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("run = %d: %s", r.code, r.body)
	}

	// Ticket 06 owns user_invites: the invited state lives on users.status and the
	// invitation row is linked by user_id.
	var status string
	if err := h.db.Raw("SELECT status FROM users WHERE email = 'alice@old.example.test'").Scan(&status).Error; err != nil {
		t.Fatalf("read users: %v", err)
	}
	if status != "invited" {
		t.Errorf("user status for alice = %q, want invited", status)
	}
	var invites int64
	if err := h.db.Raw("SELECT COUNT(*) FROM user_invites WHERE kind = 'invite' AND user_id = (SELECT id FROM users WHERE email = 'alice@old.example.test')").Scan(&invites).Error; err != nil {
		t.Fatalf("read user_invites: %v", err)
	}
	if invites != 1 {
		t.Errorf("invite rows for alice = %d, want 1", invites)
	}
	var user struct {
		Role     string `gorm:"column:role"`
		Password string `gorm:"column:password"`
	}
	h.db.Raw("SELECT role, COALESCE(password,'') AS password FROM users WHERE username = 'alice'").Scan(&user)
	if user.Password != "" {
		t.Error("imported author has a password; invited users must not have one")
	}
	if user.Role != "author" {
		t.Errorf("imported author role = %q, want author (default mapping)", user.Role)
	}
}

func TestImportAttachmentsBlockPrivateHosts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-ssrf-admin")
	token := h.token(admin)

	var hits atomic.Int64
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	}))
	defer private.Close()

	xml := impWXR(
		impPost(10, "Pic", "pic", "alice", "<p>x</p>"),
		impAttachment(30, private.URL+"/private.jpg"),
	)
	res := impUpload(h, token, "ssrf.xml", []byte(xml), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)
	if r := impCall(h, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/run", id), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("run = %d: %s", r.code, r.body)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("importer fetched a loopback attachment %d time(s)", n)
	}
	status := impCall(h, http.MethodGet, fmt.Sprintf("/api/admin/import/%d", id), "", token)
	if status.code != http.StatusOK {
		t.Fatalf("GET import = %d: %s", status.code, status.body)
	}
	if !strings.Contains(status.body, "blocked") {
		t.Errorf("import report does not say the attachment was blocked: %s", status.body)
	}
}

func TestImportCancel(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "imp-cancel-admin")
	token := h.token(admin)
	res := impUpload(h, token, "cancel.xml", []byte(impWXR(impPost(10, "Later", "later", "alice", "<p>x</p>"))), 0)
	if res.code < 200 || res.code > 299 {
		t.Fatalf("upload = %d: %s", res.code, res.body)
	}
	id := impID(t, res.body)
	if r := impCall(h, http.MethodPost, fmt.Sprintf("/api/admin/import/%d/cancel", id), "", token); r.code != http.StatusOK {
		t.Fatalf("cancel = %d, want 200: %s", r.code, r.body)
	}
	got := impCall(h, http.MethodGet, fmt.Sprintf("/api/admin/import/%d", id), "", token)
	if !strings.Contains(got.body, "cancelled") {
		t.Errorf("import state after cancel is not cancelled: %s", got.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM posts WHERE slug = 'later'"); n != 0 {
		t.Errorf("cancelled import created %d posts", n)
	}
}
