package server

// Ticket 09 G6 custom widget contract (docs/tickets/09-editor.md, API, 自訂元件, 錯誤與邊界). The admin
// registry, versions, automatic publish checks, the public sandboxed frame and the ::widget directive go
// through the real route table on the dedicated PostgreSQL database. Run with JELLY_TEST_POSTGRES=1;
// otherwise the harness skips. Written before the widget handlers exist and expected to fail against the
// 501 stubs. Do not edit them to match an implementation.
//
// Shapes the ticket leaves open, assumed here:
//   - a widget object carries id, name, version (the newest version number), status ("draft" or
//     "published"); the list carries uses, the number of posts whose source references the widget;
//   - the list body is an array or an object holding one array of widgets;
//   - a rejected write answers 400 with {"error": "...", "reasons": [...]};
//   - params is a list of {name, type, required, default, values} as the design board declares them.
//   - the publish check runs on PUT, so a dangerous source is refused at the draft stage.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// wgtSafeSource reads one declared parameter and uses none of the banned tokens.
const wgtSafeSource = "<div data-d></div>\n" +
	"<script>\n" +
	"const to = new Date(props.to);\n" +
	"document.querySelector('[data-d]').textContent = String(to.getFullYear());\n" +
	"</script>"

func wgtParams() []map[string]any {
	return []map[string]any{{"name": "to", "type": "datetime", "required": true}}
}

// wgtBody encodes a full widget write. Every field is sent each time, so a 400 can only come from the
// source under test and not from a missing field.
func wgtBody(name, source string, params []map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"name": name, "title": name, "source": source, "params": params})
	return string(raw)
}

func wgtSuccess(code int) bool {
	return code >= 200 && code <= 299
}

func wgtCreate(t *testing.T, h *accessHarness, token, name string, params []map[string]any, source string) (uint, map[string]any) {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/widgets", wgtBody(name, source, params), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create widget "+name)
	if !wgtSuccess(r.code) {
		t.Fatalf("POST /api/admin/widgets (%s) returned %d body=%q", name, r.code, r.body)
	}
	got := edtDecode(t, r.body)
	id, _ := got["id"].(float64)
	if id == 0 {
		t.Fatalf("create widget %s returned no id: %q", name, r.body)
	}
	return uint(id), got
}

func wgtUpdate(h *accessHarness, token string, id uint, name, source string, params []map[string]any) accessResult {
	return h.request(http.MethodPut, "/api/admin/widgets/"+edtID(id), wgtBody(name, source, params), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

func wgtUpdateOK(t *testing.T, h *accessHarness, token string, id uint, name, source string, params []map[string]any) map[string]any {
	t.Helper()
	r := wgtUpdate(h, token, id, name, source, params)
	h.mustNoPanic(r, "update widget "+name)
	if !wgtSuccess(r.code) {
		t.Fatalf("PUT /api/admin/widgets/%d (%s) returned %d body=%q", id, name, r.code, r.body)
	}
	return edtDecode(t, r.body)
}

func wgtPublish(h *accessHarness, token string, id uint) accessResult {
	return h.request(http.MethodPost, "/api/admin/widgets/"+edtID(id)+"/publish", "", token, echo.MIMEApplicationJSON, "")
}

func wgtPublishOK(t *testing.T, h *accessHarness, token string, id uint) map[string]any {
	t.Helper()
	r := wgtPublish(h, token, id)
	h.mustNoPanic(r, "publish widget")
	if !wgtSuccess(r.code) {
		t.Fatalf("POST /api/admin/widgets/%d/publish returned %d body=%q", id, r.code, r.body)
	}
	return edtDecode(t, r.body)
}

// wgtFrame requests the public frame of one version. It sends no credentials.
func wgtFrame(h *accessHarness, name string, v int, query string) accessResult {
	return h.request(http.MethodGet, "/widgets/"+name+"/"+strconv.Itoa(v)+"/frame"+query, "", "", "", "")
}

// wgtPostSource is a post body with one ::widget directive that sets the required parameter.
func wgtPostSource(name string) string {
	return "倒數\n\n::widget{name=" + name + " to=\"2027-01-01\"}\n"
}

// wgtAssertMount checks that rendered HTML mounts the named widget at the published version v.
func wgtAssertMount(t *testing.T, html, name string, v int) {
	t.Helper()
	version := strconv.Itoa(v)
	for _, want := range []string{
		`data-widget="` + name + `"`,
		`data-v="` + version + `"`,
		"/widgets/" + name + "/" + version + "/frame",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("widget %s at version %d must be mounted with %s: %s", name, v, want, html)
		}
	}
}

func wgtNumber(m map[string]any, key string) int {
	f, _ := m[key].(float64)
	return int(f)
}

func wgtText(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// wgtListItems returns the widget objects of GET /api/admin/widgets.
func wgtListItems(t *testing.T, body string) []map[string]any {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode widget list: %v body=%q", err, body)
	}
	list, ok := raw.([]any)
	if !ok {
		if obj, isObj := raw.(map[string]any); isObj {
			for _, v := range obj {
				if arr, isArr := v.([]any); isArr {
					list = arr
					break
				}
			}
		}
	}
	var items []map[string]any
	for _, entry := range list {
		if m, isMap := entry.(map[string]any); isMap {
			items = append(items, m)
		}
	}
	return items
}

func wgtFindByName(items []map[string]any, name string) map[string]any {
	for _, item := range items {
		if wgtText(item, "name") == name {
			return item
		}
	}
	return nil
}

func TestWidgetsAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "wgt-role-admin"))
	wid, _ := wgtCreate(t, h, adminToken, "role-w", wgtParams(), wgtSafeSource)
	id := edtID(wid)

	for _, role := range []string{"editor", "author"} {
		token := h.token(h.user(role, "wgt-role-"+role))
		routes := []struct{ method, path, body string }{
			{http.MethodGet, "/api/admin/widgets", ""},
			{http.MethodPost, "/api/admin/widgets", wgtBody("role-new", wgtSafeSource, wgtParams())},
			{http.MethodPut, "/api/admin/widgets/" + id, wgtBody("role-w", wgtSafeSource+"\n", wgtParams())},
			{http.MethodDelete, "/api/admin/widgets/" + id, ""},
			{http.MethodPost, "/api/admin/widgets/" + id + "/publish", ""},
		}
		for _, rt := range routes {
			r := h.request(rt.method, rt.path, rt.body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, role+" "+rt.method+" "+rt.path)
			if r.code != http.StatusForbidden {
				t.Errorf("%s %s %s returned %d, want 403; body=%q", role, rt.method, rt.path, r.code, r.body)
			}
		}
	}

	list := h.request(http.MethodGet, "/api/admin/widgets", "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(list, "admin list after denied writes")
	items := wgtListItems(t, list.body)
	if wgtFindByName(items, "role-w") == nil {
		t.Errorf("the widget must survive denied writes: %s", list.body)
	}
	if wgtFindByName(items, "role-new") != nil {
		t.Errorf("a denied create must not store a widget: %s", list.body)
	}
}

func TestWidgetCreatePublishAndVersion(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "wgt-version-admin"))

	id, created := wgtCreate(t, h, token, "countdown", wgtParams(), wgtSafeSource)
	if v, s := wgtNumber(created, "version"), wgtText(created, "status"); v != 1 || s != "draft" {
		t.Errorf("new widget: version=%d status=%q, want version 1 and draft", v, s)
	}

	published := wgtPublishOK(t, h, token, id)
	if s := wgtText(published, "status"); s != "published" {
		t.Errorf("publish response status = %q, want published", s)
	}

	postID, post := edtCreate(t, h, token, map[string]any{
		"title": "倒數文章", "slug": "wgt-countdown-post", "status": "published", "source": wgtPostSource("countdown"),
	})
	wgtAssertMount(t, wgtText(post, "content"), "countdown", 1)

	// Editing a published widget creates draft v2. Until v2 is published, the public frame serves v1
	// only and a re-saved post keeps mounting v1.
	edited := wgtUpdateOK(t, h, token, id, "countdown", wgtSafeSource+"\n<!-- v2 -->", wgtParams())
	if v, s := wgtNumber(edited, "version"), wgtText(edited, "status"); v != 2 || s != "draft" {
		t.Errorf("edited widget: version=%d status=%q, want version 2 and draft", v, s)
	}
	if r := wgtFrame(h, "countdown", 1, "?to=2027-01-01"); r.code != http.StatusOK {
		t.Errorf("published v1 frame returned %d while v2 is a draft, want 200", r.code)
	}
	if r := wgtFrame(h, "countdown", 2, "?to=2027-01-01"); r.code != http.StatusNotFound {
		t.Errorf("draft v2 frame returned %d, want 404", r.code)
	}
	resaved := edtMustUpdate(t, h, token, postID, map[string]any{
		"title": "倒數文章", "status": "published", "source": wgtPostSource("countdown"),
	})
	wgtAssertMount(t, wgtText(resaved, "content"), "countdown", 1)

	wgtPublishOK(t, h, token, id)
	resaved = edtMustUpdate(t, h, token, postID, map[string]any{
		"title": "倒數文章", "status": "published", "source": wgtPostSource("countdown"),
	})
	wgtAssertMount(t, wgtText(resaved, "content"), "countdown", 2)
	if r := wgtFrame(h, "countdown", 2, "?to=2027-01-01"); r.code != http.StatusOK {
		t.Errorf("published v2 frame returned %d, want 200", r.code)
	}
}

func TestWidgetAutoCheckRejectsDangerousSource(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "wgt-check-admin"))
	id, _ := wgtCreate(t, h, token, "guard", wgtParams(), wgtSafeSource)

	// Positive control: a safe edit of the same draft succeeds, so the rejections below come from the
	// dangerous source and not from the request shape.
	wgtUpdateOK(t, h, token, id, "guard", wgtSafeSource+"\n", wgtParams())

	cases := []struct {
		name   string
		source string
		params []map[string]any
	}{
		{"external script with http src", `<script src="http://evil.test/x.js"></script>`, wgtParams()},
		{"eval call", "<div data-d></div>\n<script>eval(props.to)</script>", wgtParams()},
		{"new Function call", "<div data-d></div>\n<script>new Function(\"return props.to\")()</script>", wgtParams()},
		{"source over 64 KiB", "<div data-d></div>\n<script>const pad = \"" + strings.Repeat("x", 64*1024) + "\";\nconst to = new Date(props.to);\n</script>", wgtParams()},
		{"props read without a declaration", "<div data-d></div>\n<script>const to = new Date(props.to);</script>", []map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := wgtUpdate(h, token, id, "guard", c.source, c.params)
			h.mustNoPanic(r, c.name)
			if r.code != http.StatusBadRequest {
				t.Fatalf("PUT with %s returned %d, want 400; body=%q", c.name, r.code, r.body)
			}
			got := edtDecode(t, r.body)
			reasons, ok := got["reasons"].([]any)
			if !ok || len(reasons) == 0 {
				t.Errorf("a rejected widget must list its reasons: %q", r.body)
			}
		})
	}
}

func TestWidgetFrameHeadersAndProps(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "wgt-frame-admin"))
	id, _ := wgtCreate(t, h, token, "frame-w", wgtParams(), wgtSafeSource)
	wgtPublishOK(t, h, token, id)

	r := wgtFrame(h, "frame-w", 1, "?to=2027-01-01")
	h.mustNoPanic(r, "frame v1")
	if r.code != http.StatusOK {
		t.Fatalf("GET frame returned %d, want 200; body=%q", r.code, r.body)
	}
	csp := r.header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy = %q, want it to contain %q", csp, want)
		}
	}
	if _, present := r.header["X-Frame-Options"]; present {
		t.Errorf("the frame must not send X-Frame-Options, so the post can embed it")
	}

	if bad := wgtFrame(h, "frame-w", 1, "?to=notadate"); bad.code != http.StatusBadRequest {
		t.Errorf("frame with to=notadate returned %d, want 400", bad.code)
	}
	if extra := wgtFrame(h, "frame-w", 1, "?to=2027-01-01&evil=1"); extra.code != http.StatusOK {
		t.Errorf("frame with an undeclared prop returned %d, want 200 with the prop ignored", extra.code)
	}
}

func TestWidgetDraftNotRenderedPublicly(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "wgt-draft-admin"))
	wgtCreate(t, h, token, "draft-only", wgtParams(), wgtSafeSource)

	_, created := edtCreate(t, h, token, map[string]any{
		"title": "草稿元件", "slug": "wgt-draft-post", "status": "published", "source": wgtPostSource("draft-only"),
	})
	if d, _ := created["diagnostics"].([]any); len(d) == 0 {
		t.Errorf("a post that uses a draft widget must carry a diagnostic: %v", created["diagnostics"])
	}
	content := wgtText(created, "content")
	if strings.Contains(content, "<iframe") || strings.Contains(content, "/widgets/draft-only/") {
		t.Errorf("a draft widget must not be mounted in the stored content: %s", content)
	}

	page := h.request(http.MethodGet, "/post/wgt-draft-post", "", "", "", "")
	h.mustNoPanic(page, "public draft-widget post")
	if page.code != http.StatusOK {
		t.Fatalf("public post returned %d, want 200", page.code)
	}
	if strings.Contains(page.body, "/widgets/draft-only/") {
		t.Errorf("the public post must not reference the draft widget frame")
	}

	if frame := wgtFrame(h, "draft-only", 1, "?to=2027-01-01"); frame.code != http.StatusNotFound {
		t.Errorf("frame of a draft widget returned %d, want 404", frame.code)
	}
}

func TestWidgetUsageCount(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "wgt-usage-admin"))
	wgtCreate(t, h, token, "usage-w", wgtParams(), wgtSafeSource)
	wgtCreate(t, h, token, "other-w", wgtParams(), wgtSafeSource)

	// Pages, not occurrences: the post with two usages counts once.
	edtCreate(t, h, token, map[string]any{
		"title": "用一次", "slug": "wgt-usage-a", "status": "published",
		"source": "::widget{name=usage-w to=\"2027-01-01\"}\n",
	})
	edtCreate(t, h, token, map[string]any{
		"title": "用兩次", "slug": "wgt-usage-b", "status": "published",
		"source": "::widget{name=usage-w to=\"2027-01-01\"}\n\n::widget{name=usage-w to=\"2027-02-01\"}\n",
	})
	edtCreate(t, h, token, map[string]any{
		"title": "別的元件", "slug": "wgt-usage-c", "status": "published",
		"source": "::widget{name=other-w to=\"2027-01-01\"}\n",
	})

	list := h.request(http.MethodGet, "/api/admin/widgets", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(list, "admin widget list")
	if list.code != http.StatusOK {
		t.Fatalf("GET /api/admin/widgets returned %d, want 200; body=%q", list.code, list.body)
	}
	items := wgtListItems(t, list.body)
	for name, want := range map[string]int{"usage-w": 2, "other-w": 1} {
		item := wgtFindByName(items, name)
		if item == nil {
			t.Errorf("widget %s is missing from the list: %s", name, list.body)
			continue
		}
		if got, _ := item["uses"].(float64); int(got) != want {
			t.Errorf("widget %s uses = %v, want %d pages", name, item["uses"], want)
		}
	}
}
