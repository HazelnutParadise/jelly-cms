package assetcheck

// Consistency acceptance tests for ticket 26 slot 26-B (docs/tickets/26-jelly-ui-consistency.md): every
// selection and switch control in the admin is a Jelly UI element, every notification is a Jelly toast,
// every general dialog is a <jelly-dialog>, while text inputs, buttons, navigation tabs and the ⌘K
// palette stay the project's own. They read the admin template, script and style sources, so they need no
// database; the rendered-page checks are in internal/server/jelly_ui_consistency_integration_test.go and
// the browser checks are JELLY_ONLY=consistency in scripts/acceptance/jelly-ui.spec.mjs.
// Most of these tests are expected to fail now: the pages still use .toggle-switch, native checkboxes and
// radios, alert(), .modal-overlay and native <dialog>. TestAdminNavigationTabsStayNavigation pins what must
// not change and passes now. Do not edit these tests to match an implementation.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// outOfScopeAdminTemplates are the templates under web/admin that do not use the admin layout. They do not
// load Jelly UI (ticket 12) and ticket 26 〈不做〉 leaves them alone, so no rule below applies to them.
var outOfScopeAdminTemplates = map[string]string{
	"login.html":           "登入頁：不載入 Jelly UI",
	"install.html":         "安裝頁：不載入 Jelly UI",
	"account.html":         "個人帳號頁（account_layout）：不載入 Jelly UI",
	"account_layout.html":  "個人帳號頁的版型：不載入 Jelly UI",
	"account_recover.html": "帳號復原頁：不載入 Jelly UI",
	"password_reset.html":  "重設密碼頁（account_layout）：不載入 Jelly UI",
	"invite.html":          "接受邀請頁（account_layout）：不載入 Jelly UI",
}

// consistencyAllowance lets one finding stand: a line of file that contains marker. Every entry needs a
// reason. Both lists are empty on purpose: the ⌘K palette (layout.html #palette.palette-overlay, a div with
// role="dialog") matches none of the dialog rules, and the content task lists that render
// <input type="checkbox" disabled> come from internal/markup at runtime, not from these templates.
type consistencyAllowance struct{ file, marker, reason string }

var nativeChoiceAllowlist = []consistencyAllowance{}

var nativeDialogAllowlist = []consistencyAllowance{}

type adminSource struct {
	rel     string // repository-relative, slash separated
	kind    string // "html", "js" or "css"
	content string
}

// consistencySources returns the admin templates that use the admin layout, the admin's own scripts and the
// admin's own style sheets. web/admin/assets/<dir>/ (jelly-ui, codemirror, pele, fonts) is third-party and is
// not read: the glob is not recursive.
func consistencySources(t *testing.T) []adminSource {
	t.Helper()
	root := repoRoot(t)
	var out []adminSource
	add := func(pattern, kind string) int {
		paths, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		sort.Strings(paths)
		n := 0
		for _, path := range paths {
			if kind == "html" {
				if _, skip := outOfScopeAdminTemplates[filepath.Base(path)]; skip {
					continue
				}
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			rel, _ := filepath.Rel(root, path)
			out = append(out, adminSource{rel: filepath.ToSlash(rel), kind: kind, content: string(content)})
			n++
		}
		return n
	}
	html := add(filepath.Join("web", "admin", "*.html"), "html")
	js := add(filepath.Join("web", "admin", "assets", "*.js"), "js")
	css := add(filepath.Join("web", "admin", "assets", "*.css"), "css")
	// Guard against a scan that passes because it found nothing to read.
	if html < 25 || js < 5 || css < 3 {
		t.Fatalf("read %d admin templates, %d scripts, %d style sheets; the layout changed or the globs are wrong", html, js, css)
	}
	for name := range outOfScopeAdminTemplates {
		if _, err := os.Stat(filepath.Join(root, "web", "admin", name)); err != nil {
			t.Errorf("out-of-scope template web/admin/%s is gone (%v); update outOfScopeAdminTemplates instead of widening the scan", name, err)
		}
	}
	return out
}

func sourcesOfKind(sources []adminSource, kinds ...string) []adminSource {
	var out []adminSource
	for _, s := range sources {
		for _, k := range kinds {
			if s.kind == k {
				out = append(out, s)
			}
		}
	}
	return out
}

func lineOf(content string, offset int) int { return strings.Count(content[:offset], "\n") + 1 }

func lineText(content string, offset int) string {
	start := strings.LastIndexByte(content[:offset], '\n') + 1
	end := strings.IndexByte(content[offset:], '\n')
	if end < 0 {
		end = len(content) - offset
	}
	return content[start : offset+end]
}

func snippet(content string, offset, width int) string {
	end := offset + width
	if end > len(content) {
		end = len(content)
	}
	return strings.Join(strings.Fields(content[offset:end]), " ")
}

func allowed(list []consistencyAllowance, s adminSource, offset int) bool {
	for _, a := range list {
		if a.file == s.rel && strings.Contains(lineText(s.content, offset), a.marker) {
			return true
		}
	}
	return false
}

// reportMatches reports every match of re in sources as file:line plus the matched text.
func reportMatches(t *testing.T, sources []adminSource, re *regexp.Regexp, allow []consistencyAllowance, why string) {
	t.Helper()
	var hits []string
	for _, s := range sources {
		for _, loc := range re.FindAllStringIndex(s.content, -1) {
			if allowed(allow, s, loc[0]) {
				continue
			}
			hits = append(hits, s.rel+":"+itoa(lineOf(s.content, loc[0]))+": "+snippet(s.content, loc[0], 70))
		}
	}
	if len(hits) > 0 {
		t.Errorf("%s (%d):\n%s", why, len(hits), limitLines(hits, 40))
	}
}

func limitLines(lines []string, n int) string {
	if len(lines) > n {
		lines = append(append([]string{}, lines[:n]...), "... and "+itoa(len(lines)-n)+" more")
	}
	return strings.Join(lines, "\n")
}

func isNameByte(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// tokenOffsets returns where token occurs as a whole class-like name: the bytes on either side are not
// letters, digits, '-' or '_'. So "sa-seg" matches ".sa-seg button" and class="sa-seg" but not "sa-segs".
func tokenOffsets(content, token string) []int {
	var out []int
	for from := 0; ; {
		i := strings.Index(content[from:], token)
		if i < 0 {
			return out
		}
		i += from
		end := i + len(token)
		if (i == 0 || !isNameByte(content[i-1])) && (end == len(content) || !isNameByte(content[end])) {
			out = append(out, i)
		}
		from = i + 1
	}
}

func reportTokens(t *testing.T, sources []adminSource, tokens []string, why string) {
	t.Helper()
	var hits []string
	for _, s := range sources {
		for _, token := range tokens {
			for _, off := range tokenOffsets(s.content, token) {
				hits = append(hits, s.rel+":"+itoa(lineOf(s.content, off))+": "+token+" in "+snippet(s.content, off, 60))
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("%s (%d):\n%s", why, len(hits), limitLines(hits, 40))
	}
}

var (
	templateToggle = regexp.MustCompile(`\{\{-?\s*(?:define|template|block)\s+"toggle"`)
	handMadeSwitch = regexp.MustCompile(`(?i)role\s*=\s*\\?["']?switch\b|setAttribute\(\s*['"]role['"]\s*,\s*['"]switch['"]`)
)

func TestAdminHasNoHandMadeSwitches(t *testing.T) {
	sources := consistencySources(t)
	reportTokens(t, sources, []string{"toggle-switch"},
		"the admin still has the hand-made .toggle-switch (class or CSS selector); every on/off control is <jelly-switch>")
	reportMatches(t, sourcesOfKind(sources, "html"), templateToggle, nil,
		`components.html still defines, or a page still uses, the "toggle" template; remove it, switches are <jelly-switch>`)
	reportMatches(t, sourcesOfKind(sources, "html", "js"), handMadeSwitch, nil,
		`the admin still writes its own role="switch" control (post and page editor options); use <jelly-switch>`)
}

var (
	// type="checkbox", type='radio', type=checkbox, type=\"checkbox\" in JS strings, and input.type = 'radio'.
	// The (?:^|[^\w-]) guard keeps data-type="…" and the === comparisons (el.type === 'checkbox') out.
	nativeChoiceType = regexp.MustCompile(`(?i)(?:^|[^\w-])type\s*=\s*\\?["']?\s*(?:checkbox|radio)\b`)
	nativeChoiceAttr = regexp.MustCompile(`(?i)setAttribute\(\s*['"]type['"]\s*,\s*['"](?:checkbox|radio)['"]`)
)

func TestAdminRendersNoNativeCheckboxOrRadio(t *testing.T) {
	sources := sourcesOfKind(consistencySources(t), "html", "js")
	reportMatches(t, sources, nativeChoiceType, nativeChoiceAllowlist,
		`the admin still renders, creates or selects native <input type="checkbox"> / <input type="radio"> (templates, JS strings, input.type = …, and input[type="checkbox"] selectors such as layout.html's accent-color rule); use <jelly-checkbox size="sm"> or <jelly-radio-group> / <jelly-radio size="sm">`)
	reportMatches(t, sources, nativeChoiceAttr, nativeChoiceAllowlist,
		`the admin still turns an element into a native checkbox or radio with setAttribute('type', …)`)
}

var nativeNotice = regexp.MustCompile(`(?:^|[^.\w$])(?:window\.)?(?:alert|confirm|prompt)\(`)

func TestAdminUsesNoNativeAlertConfirmOrPrompt(t *testing.T) {
	sources := sourcesOfKind(consistencySources(t), "html", "js")
	reportMatches(t, sources, nativeNotice, nil,
		"the admin still calls the browser's alert() / confirm() / prompt(); use window.Jelly.toast(message, { kind }) with an i18n message, or await Jelly.confirm({...})")
}

var (
	nativeDialogTag    = regexp.MustCompile(`(?i)<dialog[\s>/]`)
	nativeDialogCreate = regexp.MustCompile(`createElement\(\s*['"]dialog['"]\s*\)`)
	showModalCall      = regexp.MustCompile(`\.showModal\(`)
)

func TestAdminDialogsAreJellyDialogs(t *testing.T) {
	sources := consistencySources(t)
	reportTokens(t, sources, []string{"modal-overlay"},
		"the admin still has a hand-made .modal-overlay (template, script or CSS); general dialogs are <jelly-dialog>")
	code := sourcesOfKind(sources, "html", "js")
	reportMatches(t, code, nativeDialogTag, nativeDialogAllowlist,
		"the admin still has a native <dialog>; use <jelly-dialog label=…> and open it with setAttribute('open', '')")
	reportMatches(t, code, nativeDialogCreate, nativeDialogAllowlist,
		"the admin still creates a native dialog with createElement('dialog'); create a <jelly-dialog>")
	// jelly-dialog also has showModal(), but only once upgraded; the contract opens every dialog with the
	// open attribute so a call made before jelly.js runs still works.
	reportMatches(t, code, showModalCall, nativeDialogAllowlist,
		".showModal() is still called; open a <jelly-dialog> with setAttribute('open', '') and close it with removeAttribute('open')")
}

var (
	segmentedSelector = regexp.MustCompile(`\.segmented(?:[^\w-]|$)`)
	classAttrValue    = regexp.MustCompile("class(?:Name)?\\s*=\\s*\\\\?[\"'`]([^\"'`\\\\]*)")
	classListCall     = regexp.MustCompile(`classList\.\w+\(([^)]*)\)`)
	quotedSegmented   = regexp.MustCompile("[\"'`]segmented[\"'`]")
)

func TestAdminSegmentedChoicesUseNoPageOwnClasses(t *testing.T) {
	sources := consistencySources(t)
	reportTokens(t, sources, []string{"cm-seg", "rv-seg", "tr-seg", "role-tabs", "tx-seg", "sa-seg", "mn-tabs"},
		"a page still carries its own segmented-control class; every value choice or filter is <jelly-segmented> / <jelly-segment>")
	var hits []string
	for _, s := range sources {
		for _, loc := range segmentedSelector.FindAllStringIndex(s.content, -1) {
			hits = append(hits, s.rel+":"+itoa(lineOf(s.content, loc[0]))+": "+snippet(s.content, loc[0], 60))
		}
		if s.kind == "css" {
			continue
		}
		for _, m := range classAttrValue.FindAllStringSubmatchIndex(s.content, -1) {
			for _, field := range strings.Fields(s.content[m[2]:m[3]]) {
				if field == "segmented" {
					hits = append(hits, s.rel+":"+itoa(lineOf(s.content, m[0]))+": "+snippet(s.content, m[0], 60))
				}
			}
		}
		for _, m := range classListCall.FindAllStringSubmatchIndex(s.content, -1) {
			if quotedSegmented.MatchString(s.content[m[2]:m[3]]) {
				hits = append(hits, s.rel+":"+itoa(lineOf(s.content, m[0]))+": "+snippet(s.content, m[0], 60))
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("the admin still uses the hand-made .segmented class (%d); use <jelly-segmented>:\n%s", len(hits), limitLines(hits, 40))
	}
}

// usesJelly reports whether src writes the element: a start tag in a template or a JS string, or a quoted
// tag name as passed to document.createElement('jelly-checkbox') or a page's node("jelly-checkbox") helper.
func usesJelly(src, tag string) bool {
	return regexp.MustCompile("<"+regexp.QuoteMeta(tag)+`(?:[\s>/]|$)`).MatchString(src) ||
		regexp.MustCompile("[\"'`]"+regexp.QuoteMeta(tag)+"[\"'`]").MatchString(src)
}

// requiredJelly lists, per admin source, what it must contain after ticket 26 (〈逐頁清單〉). Each inner list is
// "any of"; "Jelly.toast(" stands for calling the shared notification helper.
var requiredJelly = map[string][][]string{
	"web/admin/taxonomy.html":           {{"jelly-switch"}, {"jelly-segmented"}},
	"web/admin/menus.html":              {{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}},
	"web/admin/coupons.html":            {{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}},
	"web/admin/post_editor.html":        {{"jelly-switch"}, {"jelly-segmented"}, {"jelly-checkbox"}},
	"web/admin/page_editor.html":        {{"jelly-switch"}, {"jelly-segmented"}},
	"web/admin/product_editor.html":     {{"jelly-segmented"}, {"jelly-checkbox"}},
	"web/admin/order_detail.html":       {{"jelly-segmented"}, {"jelly-checkbox"}},
	"web/admin/products.html":           {{"jelly-segmented"}},
	"web/admin/orders.html":             {{"jelly-segmented"}},
	"web/admin/comments.html":           {{"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-dialog"}},
	"web/admin/revisions.html":          {{"jelly-segmented"}, {"jelly-switch", "jelly-checkbox"}},
	"web/admin/trash.html":              {{"jelly-segmented"}, {"jelly-checkbox"}},
	"web/admin/users.html":              {{"jelly-segmented"}, {"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-dialog"}},
	"web/admin/media.html":              {{"Jelly.toast("}},
	"web/admin/plugins.html":            {{"Jelly.toast("}, {"jelly-dialog"}},
	"web/admin/settings.html":           {{"Jelly.toast(", "SA.toast("}},
	"web/admin/themes.html":             {{"jelly-checkbox"}, {"Jelly.toast("}, {"jelly-dialog"}},
	"web/admin/profile.html":            {{"jelly-checkbox", "jelly-switch"}, {"jelly-dialog"}},
	"web/admin/editor.html":             {{"jelly-dialog"}, {"jelly-radio-group"}, {"jelly-radio"}, {"jelly-checkbox"}},
	"web/admin/integrations.html":       {{"jelly-dialog"}},
	"web/admin/integration_detail.html": {{"jelly-dialog"}},
	"web/admin/assets/integrations.js":  {{"jelly-checkbox"}, {"jelly-radio-group"}, {"jelly-radio"}},
	"web/admin/assets/system-admin.js":  {{"jelly-checkbox"}, {"jelly-dialog"}},
}

// A property written to a Jelly control before jelly.js upgrades it becomes the element's own value and
// shadows the real accessor (ticket 12 〈上游事實〉 6). After ticket 26 the admin has no native checkbox or
// radio left, so any .checked or .indeterminate is either such a write or dead code, and :checked never
// matches a Jelly element. Plain-object keys count too: rename them (system-admin.js keeps field.checked).
var checkedProperty = regexp.MustCompile(`\.(?:checked|indeterminate)\b|:checked\b`)

func TestAdminPagesUseTheJellyElementsTheyNeed(t *testing.T) {
	sources := consistencySources(t)
	byPath := map[string]adminSource{}
	for _, s := range sources {
		byPath[s.rel] = s
	}
	paths := make([]string, 0, len(requiredJelly))
	for p := range requiredJelly {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		s, ok := byPath[p]
		if !ok {
			t.Errorf("%s is missing; ticket 26 〈逐頁清單〉 names it", p)
			continue
		}
		for _, anyOf := range requiredJelly[p] {
			found := false
			for _, want := range anyOf {
				if strings.HasSuffix(want, "(") {
					found = found || strings.Contains(s.content, want)
				} else {
					found = found || usesJelly(s.content, want)
				}
			}
			if !found {
				t.Errorf("%s does not use %s (ticket 26 〈逐頁清單〉)", p, strings.Join(anyOf, " or "))
			}
		}
	}
	var hits []string
	for _, s := range sourcesOfKind(sources, "html", "js") {
		for _, loc := range checkedProperty.FindAllStringIndex(s.content, -1) {
			if insideTemplateAction(s.content, loc[0]) {
				continue // {{.checked}} is a Go template field, not a DOM property
			}
			hits = append(hits, s.rel+":"+itoa(lineOf(s.content, loc[0]))+": "+snippet(s.content, loc[0], 70))
		}
	}
	if len(hits) > 0 {
		t.Errorf("the admin reads or writes .checked / .indeterminate, or selects :checked (%d); Jelly switches, checkboxes and radios are read with hasAttribute('checked') and written with toggleAttribute('checked', v) (a radio group: the radio that has [checked]):\n%s", len(hits), limitLines(hits, 40))
	}
}

// insideTemplateAction reports whether offset sits inside a {{ … }} action on its line.
func insideTemplateAction(content string, offset int) bool {
	line := content[strings.LastIndexByte(content[:offset], '\n')+1 : offset]
	return strings.LastIndex(line, "{{") > strings.LastIndex(line, "}}")
}

var (
	jellyChoiceTag = regexp.MustCompile(`<jelly-(?:checkbox|radio)(?:\s[^>]*)?>`)
	jellySizeSmall = regexp.MustCompile(`(?:^|\s)size\s*=\s*\\?["']sm\\?["']`)
	jellyNameAttr  = regexp.MustCompile(`(?:^|\s)name\s*=`)
	jellyPlainTag  = regexp.MustCompile(`<jelly-(?:switch|segmented)(?:\s[^>]*)?>`)
	anySizeAttr    = regexp.MustCompile(`(?:^|\s)size\s*=`)
	radioGroupTag  = regexp.MustCompile(`<jelly-radio-group(?:\s[^>]*)?>`)
	valueAttr      = regexp.MustCompile(`(?:^|\s)value\s*=`)
	createdChoice  = regexp.MustCompile("[\"'`]jelly-(?:checkbox|radio)[\"'`]")
	setSizeSmall   = regexp.MustCompile(`setAttribute\(\s*['"]size['"]\s*,\s*['"]sm['"]\s*\)`)
)

// The admin's checkboxes, radio groups and radios are the small size (size="sm", 28px: the default 32px box is
// larger than a switch, and the --jelly-checkbox-size variables have no effect; only the size attribute works).
// Switches and segmented controls keep the default size. A radio joins its group by its name attribute; the
// <jelly-radio-group> element only adds the radiogroup role, the legend and the size, and has no value.
func TestAdminJellyChoiceControlsAreSmallAndNamed(t *testing.T) {
	var hits []string
	for _, s := range sourcesOfKind(consistencySources(t), "html", "js") {
		for _, loc := range jellyChoiceTag.FindAllStringIndex(s.content, -1) {
			tag := s.content[loc[0]:loc[1]]
			at := s.rel + ":" + itoa(lineOf(s.content, loc[0])) + ": " + snippet(tag, 0, 90)
			if !jellySizeSmall.MatchString(tag) {
				hits = append(hits, at+` — needs size="sm"`)
			}
			if strings.HasPrefix(tag, "<jelly-radio") && !jellyNameAttr.MatchString(tag) {
				hits = append(hits, at+" — a <jelly-radio> needs name=…; radios group by name, not by <jelly-radio-group>")
			}
		}
		for _, loc := range jellyPlainTag.FindAllStringIndex(s.content, -1) {
			if tag := s.content[loc[0]:loc[1]]; anySizeAttr.MatchString(tag) {
				hits = append(hits, s.rel+":"+itoa(lineOf(s.content, loc[0]))+": "+snippet(tag, 0, 90)+" — switches and segmented controls keep the default size")
			}
		}
		for _, loc := range radioGroupTag.FindAllStringIndex(s.content, -1) {
			tag := s.content[loc[0]:loc[1]]
			at := s.rel + ":" + itoa(lineOf(s.content, loc[0])) + ": " + snippet(tag, 0, 90)
			if valueAttr.MatchString(tag) {
				hits = append(hits, at+" — <jelly-radio-group> has no value attribute upstream; mark the chosen <jelly-radio> with checked")
			}
			if !jellySizeSmall.MatchString(tag) {
				hits = append(hits, at+` — needs size="sm"`)
			}
		}
		if createdChoice.MatchString(s.content) && !setSizeSmall.MatchString(s.content) {
			hits = append(hits, s.rel+": creates <jelly-checkbox> / <jelly-radio> from script but never calls setAttribute('size', 'sm')")
		}
	}
	if len(hits) > 0 {
		t.Errorf("Jelly checkbox / radio markup does not follow the ticket 26 sizing and grouping rules (%d):\n%s", len(hits), limitLines(hits, 40))
	}
}

// notAdoptedJelly are the Jelly UI elements ticket 26 〈不做〉 keeps out of the admin.
var notAdoptedJelly = []string{"jelly-button", "jelly-input", "jelly-textarea", "jelly-select", "jelly-badge", "jelly-chip",
	"jelly-tabs", "jelly-card", "jelly-pagination", "jelly-tooltip", "jelly-menu", "jelly-drawer"}

// Navigation tab bars change the URL or the whole page section, so they stay the project's own tabs; the ⌘K
// palette stays the project's own overlay. This passes now and must keep passing.
func TestAdminNavigationTabsStayNavigation(t *testing.T) {
	root := repoRoot(t)
	settings := mustRead(t, root, "web", "admin", "settings.html")
	if !strings.Contains(settings, `<nav class="tabs"`) || !strings.Contains(settings, `{{range .Tabs}}<a class="tab`) {
		t.Error(`web/admin/settings.html lost its <nav class="tabs"> of <a class="tab"> links; the settings tab bar is navigation, not a <jelly-segmented>`)
	}
	profile := mustRead(t, root, "web", "admin", "profile.html")
	if !strings.Contains(profile, `<nav class="profile-tabs"`) || strings.Count(profile, `<a href="/admin/account?tab=`) < 6 {
		t.Error(`web/admin/profile.html lost its <nav class="profile-tabs"> of six account links; they are navigation`)
	}
	system := mustRead(t, root, "web", "admin", "assets", "system-admin.js")
	if got := strings.Count(system, `class="sys-tabs" role="tablist"`); got < 2 {
		t.Errorf(`web/admin/assets/system-admin.js has %d class="sys-tabs" role="tablist" bars, want the 2 (redirects and media policies); they are navigation`, got)
	}
	if !strings.Contains(system, `role="tab"`) {
		t.Error(`web/admin/assets/system-admin.js has no role="tab" buttons in its .sys-tabs bars any more`)
	}
	layout := mustRead(t, root, "web", "admin", "layout.html")
	for _, want := range []string{`<div class="palette-overlay" id="palette" hidden>`, `<div class="palette" role="dialog" aria-modal="true"`, `id="paletteInput"`} {
		if !strings.Contains(layout, want) {
			t.Errorf("web/admin/layout.html no longer contains %q; the ⌘K palette is not a <jelly-dialog>", want)
		}
	}
	var hits []string
	for _, s := range sourcesOfKind(consistencySources(t), "html", "js") {
		for _, tag := range notAdoptedJelly {
			if usesJelly(s.content, tag) {
				hits = append(hits, s.rel+": <"+tag+">")
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("the admin adopts Jelly UI elements ticket 26 〈不做〉 excludes:\n%s", strings.Join(hits, "\n"))
	}
}

func TestComponentsGuideDescribesTheBoundary(t *testing.T) {
	root := repoRoot(t)
	guide := mustRead(t, root, "web", "admin", "COMPONENTS.md")
	if guide == "" {
		return
	}
	for _, want := range []string{
		// the four classes of 〈界線〉
		"選擇與切換", "回饋與覆蓋層", "輸入與動作", "導覽",
		// the Jelly elements and helpers the first two classes use
		"<jelly-switch", "<jelly-segmented", "<jelly-checkbox", "<jelly-radio-group", "<jelly-radio", "<jelly-dialog",
		"Jelly.toast", "Jelly.confirm",
		// the attribute rules every page follows
		`size="sm"`, "toggleAttribute('checked'", "hasAttribute('checked')", "setAttribute('open'",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("web/admin/COMPONENTS.md does not mention %q (ticket 26: rewrite it as the 〈界線〉 rules and the Jelly element usage)", want)
		}
	}
	for _, gone := range []string{`{{template "toggle"`, "toggle-switch", "modal-overlay"} {
		if strings.Contains(guide, gone) {
			t.Errorf("web/admin/COMPONENTS.md still documents %q; that markup is removed by ticket 26", gone)
		}
	}
}
