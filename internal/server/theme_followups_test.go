package server

// File and CSS checks for ticket 10 (docs/tickets/10-theme-followups.md), issue #28. They need no database.
// Slots: 10-C (theme CSS: items 4, 6, 7 text color of <mark>, 11) and 10-E (fonts: item 10; locale keys:
// items 1 and 3). The CSS helpers at the top are also used by theme_followups_integration_test.go.
//
// These tests are expected to fail now, except the two guards that say so. Do not edit them to match an
// implementation. The CSS parser is deliberately small: it reads the <style> blocks of the theme templates,
// removes {{ ... }} actions and /* ... */ comments, flattens @media nesting and splits rules into selectors
// and declarations. It does not evaluate var() or color-mix(); contrast is a browser acceptance condition
// of the ticket, not a Go test.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type followupCSSRule struct {
	selectors []string
	decls     map[string]string
	order     int
}

var (
	followupStyleBlock     = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	followupTemplateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	followupCSSComment     = regexp.MustCompile(`(?s)/\*.*?\*/`)
	followupCSSRuleText    = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
)

// followupParseCSS parses the CSS found in the <style> elements of an HTML document or template text.
func followupParseCSS(source string) []followupCSSRule {
	var rules []followupCSSRule
	for _, block := range followupStyleBlock.FindAllStringSubmatch(source, -1) {
		css := followupCSSComment.ReplaceAllString(block[1], "")
		css = followupTemplateAction.ReplaceAllString(css, "")
		for _, m := range followupCSSRuleText.FindAllStringSubmatch(css, -1) {
			selectorText := strings.TrimSpace(m[1])
			if strings.HasPrefix(selectorText, "@") {
				continue
			}
			rule := followupCSSRule{decls: map[string]string{}, order: len(rules)}
			for _, selector := range strings.Split(selectorText, ",") {
				if selector = strings.TrimSpace(selector); selector != "" {
					rule.selectors = append(rule.selectors, selector)
				}
			}
			for _, decl := range strings.Split(m[2], ";") {
				name, value, ok := strings.Cut(decl, ":")
				if !ok {
					continue
				}
				rule.decls[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
			}
			rules = append(rules, rule)
		}
	}
	return rules
}

// followupThemeRules reads every *.html file of the default theme, in name order.
func followupThemeRules(t *testing.T) []followupCSSRule {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "web", "themes", "default", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob default theme templates: %v (%d files)", err, len(files))
	}
	var rules []followupCSSRule
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, rule := range followupParseCSS(string(content)) {
			rule.order = len(rules)
			rules = append(rules, rule)
		}
	}
	return rules
}

func (r followupCSSRule) selectorMatching(re *regexp.Regexp) (string, bool) {
	for _, selector := range r.selectors {
		if re.MatchString(selector) {
			return selector, true
		}
	}
	return "", false
}

// TestFollowupCSSParserSelfCheck keeps the helper honest. It passes now and must keep passing.
func TestFollowupCSSParserSelfCheck(t *testing.T) {
	rules := followupParseCSS(`<style>
		/* c */ .a, .b::before { color: red; margin-left: 1rem }
		@media (max-width: 1px) { .c:last-child { padding-left: 0 } }
		.d { background: {{themeColor .X "y" "#fff"}}; }
	</style>`)
	if len(rules) != 3 {
		t.Fatalf("parsed %d rules, want 3: %+v", len(rules), rules)
	}
	if len(rules[0].selectors) != 2 || rules[0].decls["margin-left"] != "1rem" {
		t.Errorf("first rule parsed wrongly: %+v", rules[0])
	}
	if rules[1].selectors[0] != ".c:last-child" || rules[1].decls["padding-left"] != "0" {
		t.Errorf("rule inside @media parsed wrongly: %+v", rules[1])
	}
}

// Issue item 4: previous/next styling must come from .post-nav-prev / .post-nav-next, not :last-child.
func TestThemeCSSPostNavUsesDirectionClasses(t *testing.T) {
	structural := regexp.MustCompile(`:(first|last|nth|nth-last)-(child|of-type)\b|:only-(child|of-type)\b`)
	var hasPrev, hasNext bool
	for _, rule := range followupThemeRules(t) {
		for _, selector := range rule.selectors {
			if strings.Contains(selector, ".post-nav-prev") {
				hasPrev = true
			}
			if strings.Contains(selector, ".post-nav-next") {
				hasNext = true
			}
			if strings.Contains(selector, "post-nav") && structural.MatchString(selector) {
				t.Errorf("selector %q styles the post navigation by position; use .post-nav-prev / .post-nav-next", selector)
			}
		}
	}
	if !hasPrev {
		t.Error("no CSS rule for .post-nav-prev in the default theme")
	}
	if !hasNext {
		t.Error("no CSS rule for .post-nav-next in the default theme")
	}
}

// Issue item 11: data-toc-level="3" entries are indented.
func TestThemeCSSTocLevelThreeIsIndented(t *testing.T) {
	levelThree := regexp.MustCompile(`\[data-toc-level\s*=\s*["']?3["']?\]`)
	zero := regexp.MustCompile(`^0(\.0+)?(px|rem|em|%)?$`)
	indent := []string{"padding-left", "padding-inline-start", "margin-left", "margin-inline-start", "text-indent"}
	found := false
	for _, rule := range followupThemeRules(t) {
		if _, ok := rule.selectorMatching(levelThree); !ok {
			continue
		}
		for _, property := range indent {
			if value, ok := rule.decls[property]; ok && value != "" && !zero.MatchString(value) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf(`no CSS rule for [data-toc-level="3"] sets a non-zero %s`, strings.Join(indent, " / "))
	}
}

// Issue item 6: <mark> must not inherit its text color (dark mode paints it gray on dark yellow, 2.1:1).
func TestThemeCSSMarkHasExplicitTextColor(t *testing.T) {
	markSelector := regexp.MustCompile(`(^|[\s>+~])mark($|[\s.:#\[>+~])`)
	inherited := map[string]bool{"inherit": true, "currentcolor": true, "unset": true, "initial": true, "revert": true}
	var seen, colored bool
	for _, rule := range followupThemeRules(t) {
		selector, ok := rule.selectorMatching(markSelector)
		if !ok {
			continue
		}
		seen = true
		value, has := rule.decls["color"]
		if has && !inherited[strings.ToLower(value)] {
			colored = true
		}
		if has && inherited[strings.ToLower(value)] {
			t.Errorf("rule for %q sets color: %s; give <mark> an explicit text color", selector, value)
		}
	}
	if !seen {
		t.Fatal("no CSS rule for <mark> in the default theme templates")
	}
	if !colored {
		t.Error("no <mark> rule sets an explicit text color")
	}
}

// Issue item 10: the fonts live only in web/admin/assets/fonts.
func TestThemeFontsKeepOnlyTheAdminCopy(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "web", "themes", "default", "assets", "fonts")); !os.IsNotExist(err) {
		t.Errorf("web/themes/default/assets/fonts must be deleted (stat error: %v)", err)
	}
	for _, name := range []string{"bricolage-grotesque.woff2", "noto-sans-tc.woff2", "LICENSE", "VERSION"} {
		if info, err := os.Stat(filepath.Join(root, "web", "admin", "assets", "fonts", name)); err != nil || info.Size() == 0 {
			t.Errorf("web/admin/assets/fonts/%s must stay: %v", name, err)
		}
	}
	script, err := os.ReadFile(filepath.Join(root, "scripts", "vendor", "fonts.sh"))
	if err != nil {
		t.Fatalf("read scripts/vendor/fonts.sh: %v", err)
	}
	if strings.Contains(string(script), "themes/default/assets") {
		t.Error("scripts/vendor/fonts.sh still writes web/themes/default/assets/fonts")
	}
	for _, file := range []string{"layout.html", "maintenance.html", "error.html"} {
		content, err := os.ReadFile(filepath.Join(root, "web", "themes", "default", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(content), "/themes/default/assets/fonts") {
			t.Errorf("%s references the deleted theme font copy", file)
		}
	}
}

// Issues items 1 and 3: the new texts are i18n keys with a Traditional Chinese and an English value.
func TestFollowupLocaleKeys(t *testing.T) {
	read := func(name string) map[string]string {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "locales", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var out map[string]string
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return out
	}
	zh, en := read("zh-TW.json"), read("en.json")
	for _, key := range []string{"errorNotFoundBody", "searchQueryTooLong"} {
		if zh[key] == "" {
			t.Errorf("web/locales/zh-TW.json has no %q", key)
		}
		if en[key] == "" {
			t.Errorf("web/locales/en.json has no %q", key)
		}
	}
	if want := followupNotFoundBodyZH; zh["errorNotFoundBody"] != want {
		t.Errorf("zh-TW errorNotFoundBody = %q, want the NotFound board text %q", zh["errorNotFoundBody"], want)
	}
	if want := "搜尋字詞最多 100 個字"; zh["searchQueryTooLong"] != want {
		t.Errorf("zh-TW searchQueryTooLong = %q, want %q", zh["searchQueryTooLong"], want)
	}
}

// followupNotFoundBodyZH is the paragraph of design/project/NotFound.dc.html.
const followupNotFoundBodyZH = "這一塊果凍掉到桌子底下了。網址可能打錯，或者文章已經被移走。"

// Issue item 8, unit level: the helper receives the escaped path. It passes now and must keep passing; the
// bug is in the middleware, which hands it the decoded path (see the integration test).
func TestFollowupMaintenanceBypassMatchesEscapedSegments(t *testing.T) {
	for path, want := range map[string]bool{
		"/admin":                       true,
		"/admin/posts":                 true,
		"/admin/assets/fonts/a.woff2":  true,
		"/api/admin/posts":             true,
		"/uploads/%E6%96%87.png":       true,
		"/login":                       true,
		"/api/oauth/status":            true,
		"/%61dmin":                     false,
		"/admin%2fx":                   false,
		"/api/%61dmin/posts":           false,
		"/api/admin%2fposts":           false,
		"/uploads%2f..%2fsecret":       false,
		"/administer":                  false,
		"/post/hello":                  false,
		"/":                            false,
		"/auth%2flogin":                false,
		"/install%2Fx":                 false,
		"/%2561dmin":                   false,
		"/admin/assets%2ffonts/x.woff": true,
	} {
		if got := maintenanceBypass(path, nil); got != want {
			t.Errorf("maintenanceBypass(%q, nil) = %v, want %v", path, got, want)
		}
	}
}
