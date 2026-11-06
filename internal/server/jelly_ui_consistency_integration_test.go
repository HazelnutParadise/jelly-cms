package server

// Jelly UI consistency acceptance tests for ticket 26 slot 26-B (docs/tickets/26-jelly-ui-consistency.md):
// every admin page an administrator opens renders its switches, value choices, checkboxes, radios and
// dialogs as Jelly UI elements, keeps the ids, names, values and defaults the page scripts and the server
// rely on, and renders none of the hand-made or native controls ticket 26 removes. They run through the
// real route table against the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1.
// The two rendering tests are expected to fail now. The Test…StaysTheSame tests are guards: they post the
// exact request bodies the current pages send (recorded in the browser on main fc6ab71) and pin what the
// server stores, so they pass now and must keep passing after the replacement. Do not edit these tests to
// match an implementation.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

// consistencyAdminPages are the 〈逐頁清單〉 pages, with the Jelly elements each response must contain (in the
// markup or in its inline scripts; each inner list is "any of"). Ids in the paths need no row: these routes
// render the template and the page script loads the record.
var consistencyAdminPages = []struct {
	path string
	need [][]string
}{
	{"/admin/taxonomy", [][]string{{"jelly-switch"}, {"jelly-segmented"}}},
	{"/admin/menus", [][]string{{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}}},
	{"/admin/coupons", [][]string{{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}}},
	{"/admin/posts/new", [][]string{{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-dialog"}}},
	{"/admin/posts/1", [][]string{{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-dialog"}}},
	{"/admin/pages/new", [][]string{{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-dialog"}}},
	{"/admin/products/new", [][]string{{"jelly-segmented"}, {"jelly-checkbox"}}},
	{"/admin/products", [][]string{{"jelly-segmented"}}},
	{"/admin/orders", [][]string{{"jelly-segmented"}}},
	{"/admin/orders/1", [][]string{{"jelly-segmented"}, {"jelly-checkbox"}}},
	{"/admin/comments", [][]string{{"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}}},
	{"/admin/posts/1/revisions", [][]string{{"jelly-segmented"}, {"jelly-switch", "jelly-checkbox"}}},
	{"/admin/trash", [][]string{{"jelly-segmented"}, {"jelly-checkbox"}}},
	{"/admin/users", [][]string{{"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-dialog"}}},
	{"/admin/media", [][]string{{"Jelly.toast("}}},
	{"/admin/plugins", [][]string{{"Jelly.toast("}, {"jelly-dialog"}}},
	{"/admin/settings/auth", nil},
	{"/admin/themes", [][]string{{"jelly-checkbox"}, {"Jelly.toast("}, {"jelly-dialog"}}},
	{"/admin/account", [][]string{{"jelly-checkbox", "jelly-switch"}, {"jelly-dialog"}}},
	{"/admin/integrations", [][]string{{"jelly-dialog"}}},
	{"/admin/integrations/1", [][]string{{"jelly-dialog"}}},
}

// consistencyUseCount counts how often body writes the element: start tags in the markup or in inline
// script strings, and quoted tag names as passed to createElement or a page's node() helper. A call such as
// "Jelly.toast(" is counted as plain text. The admin layout contributes the same count to every page (its
// Jelly.confirm builds a jelly-dialog), so callers compare a page against the dashboard.
func consistencyUseCount(body, tag string) int {
	if strings.HasSuffix(tag, "(") {
		return strings.Count(body, tag)
	}
	return len(regexp.MustCompile("<"+regexp.QuoteMeta(tag)+`(?:[\s>/]|$)`).FindAllStringIndex(body, -1)) +
		len(regexp.MustCompile("[\"'`]"+regexp.QuoteMeta(tag)+"[\"'`]").FindAllStringIndex(body, -1))
}

func consistencyIsNameByte(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// consistencyHasToken reports whether token occurs as a whole class-like name in body.
func consistencyHasToken(body, token string) bool {
	for from := 0; ; {
		i := strings.Index(body[from:], token)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(token)
		if (i == 0 || !consistencyIsNameByte(body[i-1])) && (end == len(body) || !consistencyIsNameByte(body[end])) {
			return true
		}
		from = i + 1
	}
}

// consistencyForbidden are the response patterns ticket 26 removes from every admin page (the admin layout's
// input[type="checkbox"] accent-color rule included).
var consistencyForbidden = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)(?:^|[^\w-])type\s*=\s*\\?["']?\s*(?:checkbox|radio)\b`), "a native checkbox or radio (markup, script string, input.type = … or [type=…] selector)"},
	{regexp.MustCompile(`(?i)<dialog[\s>/]`), "a native <dialog>"},
	{regexp.MustCompile(`\.showModal\(`), ".showModal()"},
	{regexp.MustCompile(`(?i)role\s*=\s*\\?["']?switch\b`), `a hand-made role="switch" control`},
	{regexp.MustCompile(`(?:^|[^.\w$])(?:window\.)?(?:alert|confirm|prompt)\(`), "a browser alert() / confirm() / prompt()"},
	{regexp.MustCompile(`\.segmented(?:[^\w-]|$)|class\s*=\s*\\?["'](?:[^"'\\]*\s)?segmented(?:\s[^"'\\]*)?\\?["']`), "the hand-made .segmented control"},
}

var consistencyForbiddenTokens = []string{"toggle-switch", "modal-overlay", "cm-seg", "rv-seg", "tr-seg", "role-tabs", "tx-seg", "sa-seg", "mn-tabs"}

func TestAdminPagesRenderOnlyJellySelectionControls(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-pages"))
	baseline := jellyUIAdminGet(t, h, token, "/admin")
	if baseline.code != http.StatusOK {
		t.Fatalf("GET /admin status = %d, want 200", baseline.code)
	}
	for _, page := range consistencyAdminPages {
		r := jellyUIAdminGet(t, h, token, page.path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", page.path, r.code)
			continue
		}
		for _, anyOf := range page.need {
			found := false
			for _, tag := range anyOf {
				found = found || consistencyUseCount(r.body, tag) > consistencyUseCount(baseline.body, tag)
			}
			if !found {
				t.Errorf("GET %s renders no %s (ticket 26 〈逐頁清單〉)", page.path, strings.Join(anyOf, " or "))
			}
		}
		for _, f := range consistencyForbidden {
			if m := f.re.FindString(r.body); m != "" {
				t.Errorf("GET %s still renders %s: %q", page.path, f.what, strings.TrimSpace(m))
			}
		}
		for _, token := range consistencyForbiddenTokens {
			if consistencyHasToken(r.body, token) {
				t.Errorf("GET %s still renders the hand-made .%s", page.path, token)
			}
		}
	}
}

// consistencyElement is one start tag of a rendered page with the start tags that contain it.
type consistencyElement struct {
	tag    string
	attrs  map[string]string
	text   string
	parent *consistencyElement
}

func (e *consistencyElement) has(name string) bool { _, ok := e.attrs[name]; return ok }

func (e *consistencyElement) closest(tag string) *consistencyElement {
	for p := e.parent; p != nil; p = p.parent {
		if p.tag == tag {
			return p
		}
	}
	return nil
}

func (e *consistencyElement) describe() string {
	var b strings.Builder
	b.WriteString("<" + e.tag)
	for _, k := range []string{"id", "name", "value", "checked", "size", "label"} {
		if v, ok := e.attrs[k]; ok {
			fmt.Fprintf(&b, " %s=%q", k, v)
		}
	}
	return b.String() + ">"
}

var consistencyVoid = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// consistencyParse returns every start tag in body, linked to its parent. Script and style contents are text
// to the tokenizer, so tags written inside JS strings are not elements here.
func consistencyParse(body string) []*consistencyElement {
	var out, stack []*consistencyElement
	z := nethtml.NewTokenizer(strings.NewReader(body))
	for tt := z.Next(); tt != nethtml.ErrorToken; tt = z.Next() {
		switch tt {
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			el := &consistencyElement{tag: string(name), attrs: map[string]string{}}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				el.attrs[string(k)] = string(v)
			}
			if len(stack) > 0 {
				el.parent = stack[len(stack)-1]
			}
			out = append(out, el)
			if tt == nethtml.StartTagToken && !consistencyVoid[el.tag] {
				stack = append(stack, el)
			}
		case nethtml.TextToken:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(z.Text())
			}
		case nethtml.EndTagToken:
			name, _ := z.TagName()
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == string(name) {
					stack = stack[:i]
					break
				}
			}
		}
	}
	return out
}

func consistencyByID(els []*consistencyElement, id string) *consistencyElement {
	for _, e := range els {
		if e.attrs["id"] == id {
			return e
		}
	}
	return nil
}

func consistencyChildrenValues(els []*consistencyElement, parent *consistencyElement, tag, closest string) []string {
	var out []string
	for _, e := range els {
		if e.tag == tag && e.closest(closest) == parent {
			out = append(out, e.attrs["value"])
		}
	}
	return out
}

// consistencySegmented finds the <jelly-segmented> by id (when key starts with '#') or by name.
func consistencySegmented(els []*consistencyElement, key string) *consistencyElement {
	for _, e := range els {
		if e.tag != "jelly-segmented" {
			continue
		}
		if strings.HasPrefix(key, "#") && e.attrs["id"] == key[1:] || !strings.HasPrefix(key, "#") && e.attrs["name"] == key {
			return e
		}
	}
	return nil
}

func TestAdminPagesRenderTheirChoicesAsJellyControls(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-markup"))
	pages := map[string][]*consistencyElement{}
	get := func(path string) []*consistencyElement {
		t.Helper()
		if els, ok := pages[path]; ok {
			return els
		}
		r := jellyUIAdminGet(t, h, token, path)
		if r.code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, r.code)
		}
		pages[path] = consistencyParse(r.body)
		return pages[path]
	}

	// On/off settings: the element keeps its id (the page script reads it) and its form name, and the
	// server-rendered default stays where the template had one.
	type control struct {
		path, id, tag, name string
		checked             int // 1 rendered checked, -1 rendered unchecked, 0 set by the page script
	}
	for _, c := range []control{
		{"/admin/taxonomy", "fHide", "jelly-switch", "hide_from_list", -1},
		{"/admin/taxonomy", "fNoCascade", "jelly-switch", "no_cascade", -1},
		{"/admin/taxonomy", "fPHidden", "jelly-switch", "pcat_hidden", -1},
		{"/admin/menus", "iNewTab", "jelly-switch", "", -1},
		{"/admin/menus", "iLoginOnly", "jelly-switch", "", -1},
		{"/admin/coupons", "fStack", "jelly-switch", "", -1},
		{"/admin/coupons", "fMembers", "jelly-switch", "", -1},
		{"/admin/coupons", "fEnabled", "jelly-switch", "", 1},
		{"/admin/posts/new", "optComments", "jelly-switch", "", 1},
		{"/admin/posts/new", "optPinned", "jelly-switch", "", -1},
		{"/admin/pages/new", "optComments", "jelly-switch", "", 1},
		{"/admin/products/new", "fShip", "jelly-checkbox", "", 0},
		{"/admin/products/new", "fCold", "jelly-checkbox", "", 0},
		{"/admin/orders/1", "rRestock", "jelly-checkbox", "", -1},
		{"/admin/comments", "selectAll", "jelly-checkbox", "", -1},
		{"/admin/comments", "sheetApprove", "jelly-checkbox", "", 1},
		{"/admin/trash", "selectAll", "jelly-checkbox", "", -1},
		{"/admin/users", "selectAllUsers", "jelly-checkbox", "", -1},
		{"/admin/posts/new", "exportFrontmatter", "jelly-checkbox", "", -1},
		{"/admin/account", "quietEnabled", "jelly-checkbox|jelly-switch", "", 0},
		{"/admin/posts/1/revisions", "collapse", "jelly-switch|jelly-checkbox", "", 1},
	} {
		e := consistencyByID(get(c.path), c.id)
		if e == nil {
			t.Errorf("GET %s has no element with id %q", c.path, c.id)
			continue
		}
		if !strings.Contains("|"+c.tag+"|", "|"+e.tag+"|") {
			t.Errorf("GET %s renders #%s as <%s>, want <%s> with the same id", c.path, c.id, e.tag, strings.ReplaceAll(c.tag, "|", "> or <"))
			continue
		}
		if c.name != "" && e.attrs["name"] != c.name {
			t.Errorf("GET %s %s name = %q, want the original form name %q", c.path, e.describe(), e.attrs["name"], c.name)
		}
		if c.checked == 1 && !e.has("checked") || c.checked == -1 && e.has("checked") {
			t.Errorf("GET %s %s: the template default must stay %s", c.path, e.describe(), map[int]string{1: "checked", -1: "unchecked"}[c.checked])
		}
		if e.tag == "jelly-checkbox" && e.attrs["size"] != "sm" {
			t.Errorf(`GET %s %s needs size="sm"`, c.path, e.describe())
		}
		if e.tag == "jelly-switch" && e.has("size") {
			t.Errorf("GET %s %s: switches keep the default size", c.path, e.describe())
		}
	}

	// Value choices and filters: <jelly-segmented> with the segment values the page script and the API use,
	// in this order, and the server-rendered default value.
	for _, c := range []struct{ path, key, value, segments string }{
		{"/admin/coupons", "kind", "percent", "percent,fixed,free_shipping,bogo"},
		{"/admin/coupons", "scope", "all", "all,category,product"},
		{"/admin/posts/new", "status", "draft", "draft,published"},
		{"/admin/pages/new", "status", "draft", "draft,published"},
		{"/admin/products/new", "status", "", "draft,active,archived"},
		{"/admin/orders/1", "mode", "full", "full,partial"},
		{"/admin/comments", "#statusSeg", "pending", "all,pending,approved,spam,trash"},
		{"/admin/trash", "#kindSeg", "all", "all,post,page,product,comment"},
		{"/admin/posts/1/revisions", "#modeSeg", "inline", "inline,only,split"},
		{"/admin/users", "#roleTabs", "", ""},
	} {
		els := get(c.path)
		seg := consistencySegmented(els, c.key)
		if seg == nil {
			t.Errorf("GET %s has no <jelly-segmented %s>", c.path, map[bool]string{true: "id=" + strings.TrimPrefix(c.key, "#"), false: "name=" + c.key}[strings.HasPrefix(c.key, "#")])
			continue
		}
		if seg.has("size") {
			t.Errorf("GET %s %s: segmented controls keep the default size", c.path, seg.describe())
		}
		if c.value != "" && seg.attrs["value"] != c.value {
			t.Errorf("GET %s %s value = %q, want the template default %q", c.path, seg.describe(), seg.attrs["value"], c.value)
		}
		if c.segments != "" {
			if got := strings.Join(consistencyChildrenValues(els, seg, "jelly-segment", "jelly-segmented"), ","); got != c.segments {
				t.Errorf("GET %s %s segments = %q, want %q", c.path, seg.describe(), got, c.segments)
			}
		}
	}
	for _, c := range []struct{ path, segments string }{
		{"/admin/taxonomy", "categories,tags,products"},
		{"/admin/menus", "page,category,post,tag,shop,custom"},
	} {
		els := get(c.path)
		found := false
		for _, seg := range els {
			if seg.tag == "jelly-segmented" && strings.Join(consistencyChildrenValues(els, seg, "jelly-segment", "jelly-segmented"), ",") == c.segments {
				found = true
			}
		}
		if !found {
			t.Errorf("GET %s has no <jelly-segmented> whose segments are %s (the values the page script switches on)", c.path, c.segments)
		}
	}

	// Radio cards: one <jelly-radio-group size="sm" label> per choice, the radios share a name, the chosen
	// one carries checked (the group has no value attribute upstream), all size="sm".
	for _, c := range []struct{ path, within, values, checked string }{
		{"/admin/users", "inviteForm", "editor,author,admin", "editor"},
		{"/admin/posts/new", "editorExport", "jmd,md,html,zip,pdf", "jmd"},
	} {
		els := get(c.path)
		scope := consistencyByID(els, c.within)
		if scope == nil {
			t.Errorf("GET %s has no #%s", c.path, c.within)
			continue
		}
		var groups, values, checked []string
		names := map[string]bool{}
		for _, e := range els {
			inside := false
			for p := e.parent; p != nil; p = p.parent {
				inside = inside || p == scope
			}
			if !inside {
				continue
			}
			switch e.tag {
			case "jelly-radio-group":
				groups = append(groups, e.describe())
				if e.attrs["size"] != "sm" || e.has("value") || strings.TrimSpace(e.attrs["label"]) == "" {
					t.Errorf(`GET %s %s inside #%s: a radio group has size="sm", a label and no value`, c.path, e.describe(), c.within)
				}
			case "jelly-radio":
				values = append(values, e.attrs["value"])
				names[e.attrs["name"]] = true
				if e.has("checked") {
					checked = append(checked, e.attrs["value"])
				}
				if e.attrs["size"] != "sm" {
					t.Errorf(`GET %s %s inside #%s needs size="sm"`, c.path, e.describe(), c.within)
				}
				if e.closest("jelly-radio-group") == nil {
					t.Errorf("GET %s %s inside #%s is not inside a <jelly-radio-group>", c.path, e.describe(), c.within)
				}
			}
		}
		if len(groups) != 1 {
			t.Errorf("GET %s #%s renders %d <jelly-radio-group>, want 1", c.path, c.within, len(groups))
		}
		if got := strings.Join(values, ","); got != c.values {
			t.Errorf("GET %s #%s <jelly-radio> values = %q, want %q", c.path, c.within, got, c.values)
		}
		if len(names) != 1 || names[""] {
			t.Errorf("GET %s #%s radios must share one non-empty name (radios group by name), got %v", c.path, c.within, names)
		}
		if got := strings.Join(checked, ","); got != c.checked {
			t.Errorf("GET %s #%s checked <jelly-radio> = %q, want exactly %q", c.path, c.within, got, c.checked)
		}
	}
	if users := get("/admin/users"); consistencyByID(users, "inviteForm") != nil {
		for _, e := range users {
			if e.tag == "jelly-radio" && e.closest("form") == consistencyByID(users, "inviteForm") && e.attrs["name"] != "role" {
				t.Errorf("GET /admin/users invite %s: the role radios keep the form name \"role\"", e.describe())
			}
		}
	}

	// Dialogs: the id the overlay or native dialog had moves to the <jelly-dialog>, and a dialog that held a
	// form still holds the same form (forms go inside the dialog: an open jelly-dialog is moved to <body>).
	for _, c := range []struct{ path, id, form string }{
		{"/admin/coupons", "batchSheet", "bf"},
		{"/admin/comments", "sheet", "sheetForm"},
		{"/admin/plugins", "uploadModal", "uploadForm"},
		{"/admin/account", "identityConfirm", ""},
		{"/admin/users", "userDialog", ""},
		{"/admin/posts/new", "editorShortcuts", ""},
		{"/admin/posts/new", "editorExport", ""},
		{"/admin/posts/new", "editorRestore", ""},
		{"/admin/pages/new", "editorExport", ""},
		{"/admin/integrations", "integrationDialog", ""},
		{"/admin/integrations/1", "integrationDialog", ""},
	} {
		els := get(c.path)
		d := consistencyByID(els, c.id)
		if d == nil || d.tag != "jelly-dialog" {
			got := "nothing"
			if d != nil {
				got = d.describe()
			}
			t.Errorf("GET %s #%s is %s, want a <jelly-dialog id=%q>", c.path, c.id, got, c.id)
			continue
		}
		if d.has("open") {
			t.Errorf("GET %s %s is rendered open", c.path, d.describe())
		}
		if c.form != "" {
			if f := consistencyByID(els, c.form); f == nil || f.tag != "form" || f.closest("jelly-dialog") != d {
				t.Errorf("GET %s <form id=%q> is not inside <jelly-dialog id=%q>", c.path, c.form, c.id)
			}
		}
	}
}

// consistencyPost sends a JSON request as the browser page does and returns the decoded response.
func consistencyPost(t *testing.T, h *accessHarness, token, method, path, body string, want int) map[string]any {
	t.Helper()
	r := h.request(method, path, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, method+" "+path)
	if r.code != want {
		t.Fatalf("%s %s = %d, want %d; body=%q", method, path, r.code, want, r.body)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(r.body), &out)
	return out
}

func consistencyID(t *testing.T, out map[string]any, what string) uint {
	t.Helper()
	if p, ok := out["product"].(map[string]any); ok {
		out = p
	}
	id, ok := out["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("%s response has no id: %v", what, out)
	}
	return uint(id)
}

// Guard: the coupon form sends this body (fixed 120 → 12000 minor units, stackable switch on). Replacing the
// type and scope controls and the three switches must not change what is posted or stored.
func TestCouponCreatePayloadStaysTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-coupon"))
	body := `{"label":"","kind":"fixed","value":12000,"buy_qty":0,"get_qty":0,"scope":"all","code":"JUICGUARD1",` +
		`"min_subtotal":0,"usage_limit":null,"per_user_limit":null,"starts_at":null,"ends_at":null,"bound_email":"",` +
		`"stackable_with_free_shipping":true,"members_only":false,"status":"active","targets":[]}`
	id := consistencyID(t, consistencyPost(t, h, token, http.MethodPost, "/api/admin/coupons", body, http.StatusOK), "coupon create")
	var c core.Coupon
	if err := h.db.First(&c, id).Error; err != nil {
		t.Fatalf("read coupon %d: %v", id, err)
	}
	if c.Code != "JUICGUARD1" || c.Kind != "fixed" || c.Value != 12000 || c.Scope != "all" || !c.StackableWithFreeShipping || c.MembersOnly ||
		c.Status != "active" || c.UsageLimit != nil || c.PerUserLimit != nil || c.StartsAt != nil || c.EndsAt != nil {
		t.Errorf("stored coupon = %+v, want the posted values", c)
	}
	patch := strings.NewReplacer(`"kind":"fixed","value":12000`, `"kind":"percent","value":15`,
		`"stackable_with_free_shipping":true,"members_only":false,"status":"active"`, `"stackable_with_free_shipping":false,"members_only":true,"status":"disabled"`).Replace(body)
	consistencyPost(t, h, token, http.MethodPatch, fmt.Sprintf("/api/admin/coupons/%d", id), patch, http.StatusOK)
	if err := h.db.First(&c, id).Error; err != nil {
		t.Fatalf("read coupon %d: %v", id, err)
	}
	if c.Kind != "percent" || c.Value != 15 || c.StackableWithFreeShipping || !c.MembersOnly || c.Status != "disabled" {
		t.Errorf("stored coupon after the edit = %+v, want kind percent 15, stackable false, members only, disabled", c)
	}
}

// Guard: the category panel sends hide_from_list and no_cascade as JSON booleans, the product-category
// panel sends hidden. Turning each switch off again must store false.
func TestCategoryFlagsStayTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-taxonomy"))
	body := `{"name":"JUI cat","slug":"jui-cat","color":"","description":"","parent_id":null,"cover":"","hide_from_list":true,"no_cascade":true}`
	id := consistencyID(t, consistencyPost(t, h, token, http.MethodPost, "/api/admin/categories", body, http.StatusCreated), "category create")
	var cat core.Category
	if err := h.db.First(&cat, id).Error; err != nil {
		t.Fatalf("read category %d: %v", id, err)
	}
	if !cat.HideFromList || !cat.NoCascade {
		t.Errorf("stored category hide_from_list=%v no_cascade=%v, want both true", cat.HideFromList, cat.NoCascade)
	}
	off := strings.Replace(body, `"hide_from_list":true,"no_cascade":true`, `"hide_from_list":false,"no_cascade":false`, 1)
	consistencyPost(t, h, token, http.MethodPut, fmt.Sprintf("/api/admin/categories/%d", id), off, http.StatusOK)
	cat = core.Category{}
	if err := h.db.First(&cat, id).Error; err != nil {
		t.Fatalf("read category %d: %v", id, err)
	}
	if cat.HideFromList || cat.NoCascade {
		t.Errorf("stored category after switching both off: hide_from_list=%v no_cascade=%v, want false", cat.HideFromList, cat.NoCascade)
	}

	pbody := `{"name":"JUI pcat","slug":"jui-pcat","description":"","parent_id":null,"cover":"","hidden":true}`
	pid := consistencyID(t, consistencyPost(t, h, token, http.MethodPost, "/api/admin/product-categories", pbody, http.StatusCreated), "product category create")
	var pc core.ProductCategory
	if err := h.db.First(&pc, pid).Error; err != nil {
		t.Fatalf("read product category %d: %v", pid, err)
	}
	if !pc.Hidden {
		t.Error("stored product category hidden = false, want true")
	}
	consistencyPost(t, h, token, http.MethodPut, fmt.Sprintf("/api/admin/product-categories/%d", pid), strings.Replace(pbody, `"hidden":true`, `"hidden":false`, 1), http.StatusOK)
	pc = core.ProductCategory{}
	if err := h.db.First(&pc, pid).Error; err != nil {
		t.Fatalf("read product category %d: %v", pid, err)
	}
	if pc.Hidden {
		t.Error("stored product category hidden after switching it off = true, want false")
	}
}

// Guard: the product editor sends the two shipping checkboxes as JSON booleans next to the status choice.
// The create below sends requires_shipping true: on main fc6ab71 a create with requires_shipping false is
// stored as true (GORM replaces the zero value with the column default; reported separately, outside ticket
// 26), so the false value is pinned on the update path, which stores it.
func TestProductShippingFlagsStayTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-product"))
	body := `{"title":"JUI product","slug":"jui-product","description":"","price":"100","compare_at_price":"","status":"draft",` +
		`"low_stock_threshold":5,"requires_shipping":true,"cold_chain":true,"images":[],"categories":[],` +
		`"variants":[{"name":"","sku":"","price":"100","compare_at_price":"","stock":0,"position":0,"status":"active"}],` +
		`"seo_title":"","seo_description":"","seo_index":"inherit","seo_ai":"inherit"}`
	id := consistencyID(t, consistencyPost(t, h, token, http.MethodPost, "/api/admin/products", body, http.StatusOK), "product create")
	read := func(stage string, ship, cold bool, status string) {
		t.Helper()
		var p core.Product
		if err := h.db.First(&p, id).Error; err != nil {
			t.Fatalf("read product %d: %v", id, err)
		}
		if p.RequiresShipping != ship || p.ColdChain != cold || p.Status != status {
			t.Errorf("%s: stored product requires_shipping=%v cold_chain=%v status=%q, want %v, %v, %q", stage, p.RequiresShipping, p.ColdChain, p.Status, ship, cold, status)
		}
	}
	read("after create", true, true, "draft")
	off := strings.NewReplacer(`"requires_shipping":true,"cold_chain":true`, `"requires_shipping":false,"cold_chain":false`, `"status":"draft"`, `"status":"active"`).Replace(body)
	consistencyPost(t, h, token, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", id), off, http.StatusOK)
	read("after unticking both", false, false, "active")
	on := strings.Replace(off, `"requires_shipping":false,"cold_chain":false`, `"requires_shipping":true,"cold_chain":true`, 1)
	consistencyPost(t, h, token, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", id), on, http.StatusOK)
	read("after ticking both again", true, true, "active")
}

// Guard: the invite form sends exactly email, role (the chosen radio card) and note.
func TestUserInviteRolePayloadStaysTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-invite"))
	out := consistencyPost(t, h, token, http.MethodPost, "/api/admin/users/invite", `{"email":"jui-invite@example.test","role":"author","note":""}`, http.StatusCreated)
	if u, _ := out["invite_url"].(string); u == "" {
		t.Errorf("invite response has no invite_url: %v", out)
	}
	var u core.User
	if err := h.db.Where("email = ?", "jui-invite@example.test").First(&u).Error; err != nil {
		t.Fatalf("read invited user: %v", err)
	}
	if u.Role != "author" || u.Status != "invited" {
		t.Errorf("invited user role=%q status=%q, want author, invited", u.Role, u.Status)
	}
}

// Guard: the theme customizer posts colors, layout and custom as JSON strings; a checked layout checkbox is
// the boolean true and an unchecked one is left out (FormData omits it), and the server keeps that shape.
func TestThemeLayoutCheckboxesStayTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-consistency-theme"))
	layout := `{"header_style":"sticky","sidebar":true,"sidebar_position":"right","container_width":"wide","post_layout":"grid"}`
	body, _ := json.Marshal(map[string]string{"colors": `{"primary":"#e8285b"}`, "layout": layout, "custom": `{}`})
	consistencyPost(t, h, token, http.MethodPost, "/api/admin/themes/default/settings", string(body), http.StatusOK)
	r := h.request(http.MethodGet, "/api/admin/themes/default/settings", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "GET theme settings")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/admin/themes/default/settings = %d; body=%q", r.code, r.body)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.body), &settings); err != nil {
		t.Fatalf("theme settings are not JSON: %v; body=%q", err, r.body)
	}
	stored := settings["layout"]
	if s, ok := stored.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			t.Fatalf("stored layout %q is not JSON: %v", s, err)
		}
		stored = decoded
	}
	got, ok := stored.(map[string]any)
	if !ok {
		t.Fatalf("stored layout = %#v, want an object", settings["layout"])
	}
	if got["sidebar"] != true {
		t.Errorf("stored layout sidebar = %#v, want the boolean true", got["sidebar"])
	}
	if _, present := got["footer"]; present {
		t.Errorf("stored layout has footer = %#v; an unchecked layout checkbox is not posted", got["footer"])
	}
}
