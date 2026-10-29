package server

import (
	"crypto/sha256"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

var adminQualityTag = regexp.MustCompile(`(?is)<([a-z][a-z0-9]*)\b([^>]*)>`)
var adminQualityAttr = regexp.MustCompile(`(?i)([a-z_:][-a-z0-9_:.]*)\s*=\s*["']([^"']*)["']`)

func adminQualityAttrs(raw string) map[string]string {
	out := map[string]string{}
	for _, match := range adminQualityAttr.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(match[1])] = html.UnescapeString(match[2])
	}
	return out
}

func adminQualityLabelledText(body, ids string) string {
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	wanted := map[string]bool{}
	for _, id := range strings.Fields(ids) {
		wanted[id] = true
	}
	var parts []string
	var walk func(*nethtml.Node, bool)
	walk = func(n *nethtml.Node, hidden bool) {
		if n.Type == nethtml.ElementNode {
			a := map[string]string{}
			for _, attr := range n.Attr {
				a[attr.Key] = attr.Val
			}
			hidden = hidden || a["aria-hidden"] == "true" || a["hidden"] != "" || a["style"] == "display:none"
			if wanted[a["id"]] && !hidden {
				var text func(*nethtml.Node)
				var b strings.Builder
				text = func(c *nethtml.Node) {
					if c.Type == nethtml.TextNode {
						b.WriteString(c.Data)
					}
					for x := c.FirstChild; x != nil; x = x.NextSibling {
						text(x)
					}
				}
				text(n)
				parts = append(parts, strings.TrimSpace(b.String()))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, hidden)
		}
	}
	walk(doc, false)
	return strings.Join(parts, " ")
}

func adminQualityInputLabel(body, inputID string) string {
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	var walk func(*nethtml.Node) string
	walk = func(n *nethtml.Node) string {
		if n.Type == nethtml.ElementNode && n.Data == "label" {
			for _, attr := range n.Attr {
				if attr.Key == "for" && attr.Val == inputID {
					var b strings.Builder
					var text func(*nethtml.Node)
					text = func(c *nethtml.Node) {
						if c.Type == nethtml.TextNode {
							b.WriteString(c.Data)
						}
						for x := c.FirstChild; x != nil; x = x.NextSibling {
							text(x)
						}
					}
					text(n)
					return strings.TrimSpace(b.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if found := walk(c); found != "" {
				return found
			}
		}
		return ""
	}
	return walk(doc)
}

func TestAdminQualityTranslatedCreatePagesAndSettingsNavigation(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "admin-quality-pages")
	token := h.token(admin)
	for _, lang := range []struct{ query, postTitle, pageTitle, newPost string }{
		{"zh-TW", "文章編輯器", "頁面編輯器", "新增文章"},
		{"en", "Post editor", "Page editor", "New Post"},
	} {
		posts := h.request(http.MethodGet, "/admin/posts?lang="+lang.query, "", token, echo.MIMETextHTML, "")
		if posts.code != http.StatusOK || !strings.Contains(posts.body, ">"+lang.newPost+"</a>") {
			t.Errorf("GET /admin/posts?lang=%s did not render translated New Post action: status=%d", lang.query, posts.code)
		}
		for _, path := range []string{"/admin/posts/new", "/admin/pages/new"} {
			page := h.request(http.MethodGet, path+"?lang="+lang.query, "", token, echo.MIMETextHTML, "")
			if page.code != http.StatusOK {
				t.Errorf("GET %s?lang=%s status=%d, want 200", path, lang.query, page.code)
				continue
			}
			want := lang.postTitle
			if strings.Contains(path, "/pages/") {
				want = lang.pageTitle
			}
			if !strings.Contains(page.body, "<title>"+want+" · Jelly CMS</title>") {
				t.Errorf("GET %s?lang=%s title lacks %q", path, lang.query, want)
			}
			// Title field IDs and technical slug examples differ between post and page editors.
			titleID, titlePlaceholder := "postTitle", "標題"
			titleLabel := "標題"
			if strings.Contains(path, "/pages/") {
				titleID, titlePlaceholder = "pageTitle", "頁面標題"
			}
			if lang.query == "en" {
				titleLabel = "Title"
				titlePlaceholder = "Title"
				if strings.Contains(path, "/pages/") {
					titlePlaceholder = "Page title"
				}
			}
			if got := adminQualityInputLabel(page.body, titleID); got != titleLabel {
				t.Errorf("GET %s?lang=%s title label=%q, want %q", path, lang.query, got, titleLabel)
			}
			if !strings.Contains(page.body, `id="`+titleID+`"`) {
				t.Errorf("GET %s has no title input", path)
			}
			// The title placeholder must match the current language; technical slug examples remain untouched.
			wantPlaceholder := titlePlaceholder
			if !strings.Contains(page.body, `id="`+titleID+`"`) || !strings.Contains(page.body, `placeholder="`+wantPlaceholder+`"`) {
				t.Errorf("GET %s?lang=%s title label/placeholder is not localized to %q", path, lang.query, wantPlaceholder)
			}
		}
	}
	settings := h.request(http.MethodGet, "/admin/settings/shop", "", token, echo.MIMETextHTML, "")
	if settings.code != http.StatusOK {
		t.Fatalf("GET /admin/settings/shop status=%d", settings.code)
	}
	navs, falseTabs, active := 0, 0, 0
	settingsNav := regexp.MustCompile(`(?is)<nav\b[^>]*class="tabs"[^>]*>.*?</nav>`).FindString(settings.body)
	for _, m := range adminQualityTag.FindAllStringSubmatch(settingsNav, -1) {
		a := adminQualityAttrs(m[2])
		if m[1] == "nav" && strings.Contains(m[2], `class="tabs"`) && a["aria-label"] != "" {
			navs++
		}
		if a["role"] == "tablist" || a["role"] == "tab" || a["aria-selected"] != "" {
			falseTabs++
		}
		if a["aria-current"] == "page" && a["href"] != "" {
			active++
		}
	}
	if navs == 0 || falseTabs != 0 || active == 0 {
		t.Errorf("cross-page settings navigation semantics: named nav=%d false tab semantics=%d active link=%d", navs, falseTabs, active)
	}
}

func TestAdminQualityHeaderNamesAndFaviconHashRedirect(t *testing.T) {
	h := newWebMCPHarness(t)
	previousUnread := UnreadNotifications
	t.Cleanup(func() { UnreadNotifications = previousUnread })
	for _, count := range []int{0, 7, 100} {
		UnreadNotifications = func(_ echo.Context, _ uint) int { return count }
		page := h.request(http.MethodGet, "/admin/posts?lang=en", "", h.token, echo.MIMETextHTML, "")
		if page.code != http.StatusOK {
			t.Fatalf("GET admin with unread=%d status=%d", count, page.code)
		}
		paletteNamed, bellNamed := false, false
		for _, m := range adminQualityTag.FindAllStringSubmatch(page.body, -1) {
			a := adminQualityAttrs(m[2])
			if a["id"] == "paletteTrigger" {
				name := a["aria-label"]
				if a["aria-labelledby"] != "" {
					name = adminQualityLabelledText(page.body, a["aria-labelledby"])
				}
				paletteNamed = strings.Contains(name, "Ctrl") && strings.Contains(name, "K") || strings.Contains(name, "⌘") && strings.Contains(name, "K")
			}
			if a["href"] == "/admin/notifications" {
				name := a["aria-label"]
				if a["aria-labelledby"] != "" {
					name = adminQualityLabelledText(page.body, a["aria-labelledby"])
				}
				wantCount := fmt.Sprint(count)
				if count > 99 {
					wantCount = "99+"
				}
				bellNamed = name != "" && (count == 0 || strings.Contains(name, wantCount))
			}
		}
		if !paletteNamed {
			t.Errorf("palette trigger omits its visible shortcut from its accessible name")
		}
		if !bellNamed {
			t.Errorf("notification link name does not expose count %d", count)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := h.request(method, "/favicon.ico", "", "", "", "")
		if r.code != http.StatusTemporaryRedirect {
			t.Errorf("%s /favicon.ico status=%d, want 307", method, r.code)
			continue
		}
		location, err := url.Parse(r.header.Get(echo.HeaderLocation))
		if err != nil || location.Host != "" || location.Path != "/admin/assets/jelly-mark.svg" || len(location.Query()["v"]) != 1 || !strings.Contains(strings.ToLower(r.header.Get(echo.HeaderCacheControl)), "no-cache") {
			t.Errorf("%s /favicon.ico Location/Cache-Control = %q/%q", method, r.header.Get(echo.HeaderLocation), r.header.Get(echo.HeaderCacheControl))
		}
		assetMethod := method
		if method == http.MethodHead {
			if r.body != "" {
				t.Errorf("HEAD /favicon.ico returned a body of %d bytes", len(r.body))
			}
			assetMethod = http.MethodGet
		}
		asset := h.request(assetMethod, r.header.Get(echo.HeaderLocation), "", "", "", "")
		if asset.code != http.StatusOK {
			t.Errorf("%s favicon asset status=%d, want 200", method, asset.code)
			continue
		}
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(asset.body)))
		if location.Query().Get("v") != want {
			t.Errorf("favicon hash %q does not match exact asset bytes %q", location.Query().Get("v"), want)
		}
		if !strings.Contains(strings.ToLower(asset.header.Get(echo.HeaderContentType)), "image/svg+xml") {
			t.Errorf("favicon MIME=%q", asset.header.Get(echo.HeaderContentType))
		}
		cache := asset.header.Get(echo.HeaderCacheControl)
		if !strings.Contains(cache, "immutable") || !strings.Contains(cache, "max-age=31536000") {
			t.Errorf("favicon cache policy=%q", cache)
		}
	}
	bad := h.request(http.MethodGet, "/admin/assets/jelly-mark.svg?v="+strings.Repeat("0", 64), "", "", "", "")
	if bad.code != http.StatusNotFound || !strings.Contains(strings.ToLower(bad.header.Get(echo.HeaderCacheControl)), "no-store") {
		t.Errorf("wrong favicon hash status/cache=%d/%q", bad.code, bad.header.Get(echo.HeaderCacheControl))
	}
	adminPage := h.request(http.MethodGet, "/admin/posts", "", h.token, echo.MIMETextHTML, "")
	if adminPage.code != http.StatusOK {
		t.Fatalf("GET /admin/posts status=%d", adminPage.code)
	}
	iconOK := false
	for _, m := range adminQualityTag.FindAllStringSubmatch(adminPage.body, -1) {
		a := adminQualityAttrs(m[2])
		if m[1] == "link" && a["rel"] == "icon" && strings.Contains(a["href"], "/admin/assets/jelly-mark.svg?v=") && len(strings.TrimPrefix(a["href"], "/admin/assets/jelly-mark.svg?v=")) == 64 {
			iconOK = true
		}
	}
	if !iconOK {
		t.Errorf("admin HTML does not reference hash-versioned jelly-mark.svg favicon")
	}
}
