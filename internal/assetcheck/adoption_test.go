package assetcheck

// Adoption acceptance tests for ticket 12 slots Z3 to Z6 (docs/tickets/12-zero-cdn-jelly-ui.md): which
// templates load Jelly UI and which controls become Jelly UI elements. They read the template sources, so
// they need no database; the rendered-page checks are in internal/server/jelly_ui_integration_test.go.
// These tests are expected to fail now: no template uses Jelly UI yet. Do not edit them to match an
// implementation.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// moduleTag is the one tag that loads Jelly UI; the admin layout carries it and nothing else does.
const moduleTag = `<script type="module" src="/admin/assets/jelly-ui/jelly.js"></script>`

// startTags returns the attributes of every start tag called name in an HTML or Go template source.
func startTags(src, name string) []map[string]string {
	var out []map[string]string
	z := html.NewTokenizer(strings.NewReader(src))
	for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tag, hasAttr := z.TagName()
		if string(tag) != name {
			continue
		}
		attrs := map[string]string{}
		for hasAttr {
			var k, v []byte
			k, v, hasAttr = z.TagAttr()
			attrs[string(k)] = string(v)
		}
		out = append(out, attrs)
	}
	return out
}

func TestOnlyTheAdminLayoutLoadsJellyUI(t *testing.T) {
	root := repoRoot(t)
	layout := mustRead(t, root, "web", "admin", "layout.html")
	if got := strings.Count(layout, moduleTag); got != 1 {
		t.Errorf("web/admin/layout.html contains the Jelly UI module tag %d times, want exactly 1: %s", got, moduleTag)
	}

	var others []string
	err := filepath.WalkDir(filepath.Join(root, "web"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if filepath.ToSlash(rel) != "web/admin/layout.html" {
			others = append(others, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk web: %v", err)
	}
	for _, path := range others {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(content), "jelly-ui/jelly.js") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s loads Jelly UI; only web/admin/layout.html may (login, install, account and public themes do not)", filepath.ToSlash(rel))
		}
	}
}

func TestAdminLayoutMapsTokensAndDefinesTheHelpers(t *testing.T) {
	root := repoRoot(t)
	layout := mustRead(t, root, "web", "admin", "layout.html")
	if layout == "" {
		return
	}
	for _, want := range []string{
		// token mapping onto the existing variables
		"--jelly-color-background-surface:", "--jelly-color-background-accent:", "--jelly-color-foreground-default:",
		"--jelly-color-foreground-muted:", "--jelly-color-border-default:",
		"--jelly-font-display:", "--jelly-font-text:", "--jelly-font-mono:",
		// diagonal corner for the two components that expose a radius variable
		"--jelly-dialog-radius:", "--jelly-toast-radius:",
		// light and dark mirror
		"data-jelly-mode", "jelly-theme-change",
		// the toaster host replaces the hand-made .toasts container
		`<jelly-toaster position="bottom"`,
		// helpers
		"window.Jelly.toast", "window.Jelly.confirm", "jellyToast",
	} {
		if !strings.Contains(layout, want) {
			t.Errorf("web/admin/layout.html does not contain %q", want)
		}
	}
	for _, gone := range []string{`class="toasts"`, ".toast-dot", "toastRoot"} {
		if strings.Contains(layout, gone) {
			t.Errorf("web/admin/layout.html still carries the old toast implementation (%q); keep one toast, not two", gone)
		}
	}
}

var nativeConfirm = regexp.MustCompile(`(?:^|[^.\w$])(?:window\.)?confirm\(`)

func TestAdminPagesHaveNoNativeOrHandMadeConfirmDialogs(t *testing.T) {
	root := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(root, "web", "admin", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no admin templates found (err %v)", err)
	}
	sort.Strings(pages)
	for _, page := range pages {
		content, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		name := filepath.Base(page)
		for i, line := range strings.Split(string(content), "\n") {
			if nativeConfirm.MatchString(line) {
				t.Errorf("web/admin/%s:%d calls the browser confirm(); use `await Jelly.confirm({...})`", name, i+1)
			}
		}
		for _, hand := range []string{"confirmSheet", "tr-dlg", "rv-dlg", "tr-overlay", "rv-overlay"} {
			if strings.Contains(string(content), hand) {
				t.Errorf("web/admin/%s still carries its own confirm dialog (%q); use Jelly.confirm", name, hand)
			}
		}
	}
}

// settingsSwitches pins the id each boolean keeps and the form name it gains. The id stays because the
// page script reads fields by id; the name is what a native form would submit.
var settingsSwitches = map[string]string{
	"googleEnabled":    "google_enabled",
	"githubEnabled":    "github_enabled",
	"ecpayEnabled":     "ecpay_enabled",
	"ecpayTestMode":    "ecpay_test_mode",
	"newebpayEnabled":  "newebpay_enabled",
	"newebpayTestMode": "newebpay_test_mode",
}

func TestSettingsBooleansAreJellySwitches(t *testing.T) {
	root := repoRoot(t)
	settings := mustRead(t, root, "web", "admin", "settings.html")
	if settings == "" {
		return
	}
	if strings.Contains(settings, `type="checkbox"`) || strings.Contains(settings, "toggle-switch") {
		t.Error("web/admin/settings.html still has a checkbox or .toggle-switch; every boolean is a <jelly-switch>")
	}
	found := map[string]map[string]string{}
	for _, attrs := range startTags(settings, "jelly-switch") {
		found[attrs["id"]] = attrs
	}
	for id, name := range settingsSwitches {
		attrs, ok := found[id]
		if !ok {
			t.Errorf("web/admin/settings.html has no <jelly-switch id=%q>", id)
			continue
		}
		if attrs["name"] != name {
			t.Errorf("<jelly-switch id=%q> has name=%q, want %q", id, attrs["name"], name)
		}
		if strings.TrimSpace(attrs["label"]) == "" {
			t.Errorf("<jelly-switch id=%q> has no label attribute; the switch is its own accessible name", id)
		}
	}
	// A jelly-switch the browser has not upgraded yet would keep a property written to it as its own value
	// and shadow the real one, so the page writes the attribute and never assigns .checked on a switch.
	if m := regexp.MustCompile(`(?:Enabled|TestMode)['"]\)\.checked\s*=`).FindString(settings); m != "" {
		t.Errorf("web/admin/settings.html assigns .checked on a jelly-switch (%q); use toggleAttribute('checked', value)", m)
	}
}

func TestPostsListHasAStatusSegmentedControl(t *testing.T) {
	root := repoRoot(t)
	posts := mustRead(t, root, "web", "admin", "posts.html")
	if posts == "" {
		return
	}
	controls := startTags(posts, "jelly-segmented")
	if len(controls) != 1 {
		t.Fatalf("web/admin/posts.html has %d <jelly-segmented>, want 1 (the status filter)", len(controls))
	}
	if controls[0]["name"] != "status" {
		t.Errorf("<jelly-segmented> name = %q, want %q", controls[0]["name"], "status")
	}
	if strings.TrimSpace(controls[0]["label"]) == "" {
		t.Error("<jelly-segmented> has no label attribute; the group needs an accessible name")
	}
	var values []string
	for _, attrs := range startTags(posts, "jelly-segment") {
		values = append(values, attrs["value"])
	}
	if got, want := strings.Join(values, ","), "all,published,draft,pending"; got != want {
		t.Errorf("<jelly-segment> values = %q, want %q in this order", got, want)
	}
}
