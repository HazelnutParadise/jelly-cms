package server

// Offline HTML export regression tests. These exercise the public export route
// against the dedicated PostgreSQL harness and inspect nested iframe documents
// as HTML rather than matching escaped srcdoc strings.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"golang.org/x/net/html"
)

func exportOfflineDocument(t *testing.T, h *accessHarness, token string, id uint) *html.Node {
	t.Helper()
	r := edtExport(h, token, id, "format=html")
	h.mustNoPanic(r, "offline HTML export")
	if r.code != http.StatusOK {
		t.Fatalf("HTML export returned %d body=%q", r.code, r.body)
	}
	doc, err := html.Parse(strings.NewReader(r.body))
	if err != nil {
		t.Fatalf("parse exported HTML: %v", err)
	}
	return doc
}

func exportOfflineElements(root *html.Node, tag string) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			found = append(found, n)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}

func exportOfflineAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func exportOfflineText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func TestExportOfflineBuiltinCountdownHasVisibleFallbackAndEnglishRuntime(t *testing.T) {
	h := newAccessHarness(t)
	user := h.user("admin", "offline-countdown")
	if err := h.db.Model(&core.User{}).Where("id = ?", user.ID).Update("locale", "en").Error; err != nil {
		t.Fatalf("set English locale: %v", err)
	}
	token := h.token(user)
	id, _ := edtCreate(t, h, token, map[string]any{
		"title":  "Offline countdown",
		"source": "# Deadline\n\nBefore the deadline.\n\n::widget{name=countdown to=\"2027-01-02T03:04:00Z\"}\n",
	})

	doc := exportOfflineDocument(t, h, token, id)
	frames := exportOfflineElements(doc, "iframe")
	if len(frames) != 1 {
		t.Fatalf("export has %d widget iframes, want one", len(frames))
	}
	frame := frames[0]
	if src := exportOfflineAttr(frame, "src"); src != "" {
		t.Errorf("offline frame still depends on a route that is unavailable from file://: src=%q", src)
	}
	if sandbox := exportOfflineAttr(frame, "sandbox"); sandbox != "allow-scripts" {
		t.Errorf("countdown sandbox = %q, want exactly allow-scripts", sandbox)
	}
	srcdoc := exportOfflineAttr(frame, "srcdoc")
	if srcdoc == "" {
		t.Fatal("countdown iframe has no self-contained srcdoc")
	}
	child, err := html.Parse(strings.NewReader(srcdoc))
	if err != nil {
		t.Fatalf("parse countdown srcdoc: %v", err)
	}
	times := exportOfflineElements(child, "time")
	if len(times) != 1 || exportOfflineAttr(times[0], "datetime") != "2027-01-02T03:04:00Z" {
		t.Errorf("offline countdown lacks its visible no-JavaScript deadline fallback: %s", srcdoc)
	} else if strings.TrimSpace(exportOfflineText(times[0])) == "" {
		t.Errorf("countdown deadline time has no visible fallback text")
	}
	if !strings.Contains(srcdoc, "Remaining ") || !strings.Contains(srcdoc, " days ") {
		t.Errorf("English countdown labels were not preserved in the child runtime")
	}
	metas := exportOfflineElements(child, "meta")
	var csp string
	for _, meta := range metas {
		if strings.EqualFold(exportOfflineAttr(meta, "http-equiv"), "content-security-policy") {
			csp = exportOfflineAttr(meta, "content")
		}
	}
	for _, want := range []string{"default-src 'none'", "script-src 'unsafe-inline'", "style-src 'unsafe-inline'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("child CSP %q does not include %q", csp, want)
		}
	}
}

func TestExportOfflineCustomWidgetUsesPinnedPublishedSourceAfterSoftDelete(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-custom-widget"))
	params := []map[string]any{{"name": "label", "type": "string", "required": true}}
	v1 := `<main>V1_CUSTOM_BODY <b id="label"></b></main><script>document.querySelector('#label').textContent=props.label</script>`
	widgetID, _ := wgtCreate(t, h, token, "offline-card", params, v1)
	wgtPublishOK(t, h, token, widgetID)
	id, _ := edtCreate(t, h, token, map[string]any{
		"title":  "Pinned widget export",
		"source": "Outer post text.\n\n::widget{name=offline-card label=\"typed value\"}\n",
	})
	wgtUpdateOK(t, h, token, widgetID, "offline-card", `<main>V2_NEWER_BODY</main>`, params)
	wgtPublishOK(t, h, token, widgetID)
	var widget core.Widget
	if err := h.db.First(&widget, widgetID).Error; err != nil {
		t.Fatalf("load widget for soft-delete fixture: %v", err)
	}
	if err := h.db.Delete(&widget).Error; err != nil {
		t.Fatalf("soft-delete widget after publishing newer version: %v", err)
	}

	doc := exportOfflineDocument(t, h, token, id)
	frames := exportOfflineElements(doc, "iframe")
	if len(frames) != 1 {
		t.Fatalf("export has %d custom widget iframes, want one", len(frames))
	}
	frame := frames[0]
	if src := exportOfflineAttr(frame, "src"); strings.Contains(src, "/widgets/") {
		t.Errorf("custom widget still requires the online frame endpoint after export: %q", src)
	}
	if exportOfflineAttr(frame, "sandbox") != "allow-scripts" {
		t.Errorf("custom widget sandbox = %q, want exactly allow-scripts", exportOfflineAttr(frame, "sandbox"))
	}
	srcdoc := exportOfflineAttr(frame, "srcdoc")
	if srcdoc == "" {
		t.Fatal("custom widget iframe has no self-contained srcdoc")
	}
	if !strings.Contains(srcdoc, "V1_CUSTOM_BODY") || strings.Contains(srcdoc, "V2_NEWER_BODY") {
		t.Errorf("export did not preserve the page's pinned published source: %s", srcdoc)
	}
	if strings.Count(srcdoc, "const props=") != 1 || !strings.Contains(srcdoc, `"label":"typed value"`) {
		t.Errorf("export must inject the validated typed props exactly once: %s", srcdoc)
	}
	if strings.Contains(exportOfflineText(doc), "V1_CUSTOM_BODY") || strings.Contains(exportOfflineText(doc), "V2_NEWER_BODY") {
		t.Errorf("custom widget source escaped its iframe into the outer document")
	}
}

func TestExportOfflineSameNameCustomCountdownTakesPriority(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-custom-countdown"))
	widgetID, _ := wgtCreate(t, h, token, "countdown", []map[string]any{}, `<p>CUSTOM_COUNTDOWN_OVERRIDE</p>`)
	wgtPublishOK(t, h, token, widgetID)
	id, _ := edtCreate(t, h, token, map[string]any{
		"title":  "Custom countdown override",
		"source": `::widget{name=countdown}`,
	})
	doc := exportOfflineDocument(t, h, token, id)
	frames := exportOfflineElements(doc, "iframe")
	if len(frames) != 1 {
		t.Fatalf("export has %d countdown frames, want one", len(frames))
	}
	srcdoc := exportOfflineAttr(frames[0], "srcdoc")
	if !strings.Contains(srcdoc, "CUSTOM_COUNTDOWN_OVERRIDE") {
		t.Errorf("offline export did not resolve the published custom countdown over the builtin: %s", srcdoc)
	}
}

func TestExportOfflineNativeFeaturesAndNoWidgetDocumentRemainUsable(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-native-features"))
	source := "# Motion heading{reveal=fade-up}\n\n" +
		":::tabs\n::: tab {title=One}\nFirst panel\n:::\n::: tab {title=Two}\nSecond panel\n:::\n:::\n\n" +
		"::compare{before=/uploads/before.png after=/uploads/after.png}\n\n$x^2$\n"
	id, _ := edtCreate(t, h, token, map[string]any{"title": "Native features", "source": source})
	r := edtExport(h, token, id, "format=html")
	h.mustNoPanic(r, "offline native feature export")
	if r.code != http.StatusOK {
		t.Fatalf("HTML export returned %d body=%q", r.code, r.body)
	}
	doc, err := html.Parse(strings.NewReader(r.body))
	if err != nil {
		t.Fatalf("parse exported HTML: %v", err)
	}
	bodyText := exportOfflineText(doc)
	for _, want := range []string{"First panel", "Second panel", "data-reveal", "data-jc=\"tabs\"", "jc-compare", "jc-math"} {
		if !strings.Contains(r.body, want) && !strings.Contains(bodyText, want) {
			t.Errorf("offline export lost native feature %q", want)
		}
	}
	styleText := ""
	for _, style := range exportOfflineElements(doc, "style") {
		styleText += exportOfflineText(style)
	}
	if !strings.Contains(styleText, "--primary:") || !strings.Contains(styleText, "--surface:") || !strings.Contains(styleText, "--ink:") {
		t.Errorf("standalone export omits fixed theme variables needed by content.css")
	}
	if !strings.Contains(styleText, "data:font/woff2;base64,") || strings.Contains(strings.ToLower(styleText), "url(http") {
		t.Errorf("standalone export math stylesheet must use bundled inline fonts only")
	}
	var scriptText strings.Builder
	for _, script := range exportOfflineElements(doc, "script") {
		if exportOfflineAttr(script, "src") != "" {
			t.Errorf("standalone export depends on external script %q", exportOfflineAttr(script, "src"))
		}
		scriptText.WriteString(exportOfflineText(script))
	}
	if !strings.Contains(scriptText.String(), "trust:false") || !strings.Contains(scriptText.String(), "tabs") || !strings.Contains(scriptText.String(), "compare") || !strings.Contains(scriptText.String(), "IntersectionObserver") {
		t.Errorf("standalone export is missing the local interactive or trust:false math runtime")
	}
	for _, link := range exportOfflineElements(doc, "link") {
		if exportOfflineAttr(link, "href") != "" {
			t.Errorf("standalone export depends on external stylesheet %q", exportOfflineAttr(link, "href"))
		}
	}

	plainID, _ := edtCreate(t, h, token, map[string]any{"title": "No widgets", "source": "Plain offline paragraph."})
	plain := edtExport(h, token, plainID, "format=html")
	if plain.code != http.StatusOK || !strings.Contains(plain.body, "Plain offline paragraph.") {
		t.Errorf("HTML export without widgets regressed: status=%d body=%q", plain.code, plain.body)
	}
}

func TestExportOfflineMissingReferencedWidgetVersionReturnsError(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-missing-version"))
	id, _ := edtCreate(t, h, token, map[string]any{"title": "Missing widget version", "source": "A post."})
	broken := `<p>Content before missing widget.</p><div class="jc-widget" data-widget="missing-widget" data-v="99"><iframe sandbox="allow-scripts" src="/widgets/missing-widget/99/frame"></iframe></div>`
	if err := h.db.Model(&core.Post{}).Where("id = ?", id).Update("content", broken).Error; err != nil {
		t.Fatalf("seed post with unavailable immutable widget reference: %v", err)
	}
	r := edtExport(h, token, id, "format=html")
	h.mustNoPanic(r, "HTML export with missing widget version")
	if r.code < http.StatusBadRequest {
		t.Errorf("HTML export silently returned %d for a missing referenced immutable version (body length %d)", r.code, len(r.body))
	}
}

func TestExportOfflineInvalidReferencedWidgetParamsReturnError(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-invalid-widget-params"))
	widgetID, _ := wgtCreate(t, h, token, "offline-required-parameter", []map[string]any{{"name": "label", "type": "string", "required": true}}, `<p>PARAMETER_WIDGET</p>`)
	wgtPublishOK(t, h, token, widgetID)
	id, _ := edtCreate(t, h, token, map[string]any{"title": "Invalid widget params", "source": "A post."})
	broken := `<div class="jc-widget" data-widget="offline-required-parameter" data-v="1"><iframe sandbox="allow-scripts" src="/widgets/offline-required-parameter/1/frame"></iframe></div>`
	if err := h.db.Model(&core.Post{}).Where("id = ?", id).Update("content", broken).Error; err != nil {
		t.Fatalf("seed post with invalid widget parameters: %v", err)
	}
	r := edtExport(h, token, id, "format=html")
	h.mustNoPanic(r, "HTML export with invalid widget parameters")
	if r.code < http.StatusBadRequest {
		t.Errorf("HTML export silently returned %d for invalid required widget parameters (body length %d)", r.code, len(r.body))
	}
}

func TestExportOfflineCapsExpandedWidgetDocumentSize(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "offline-export-size"))
	largeSource := `<main>` + strings.Repeat("x", 60<<10) + `</main>`
	widgetID, _ := wgtCreate(t, h, token, "offline-large-widget", []map[string]any{}, largeSource)
	wgtPublishOK(t, h, token, widgetID)
	var source strings.Builder
	for range 150 {
		source.WriteString("::widget{name=offline-large-widget}\n")
	}
	id, _ := edtCreate(t, h, token, map[string]any{"title": "Expanded document size", "source": source.String()})
	r := edtExport(h, token, id, "format=html")
	h.mustNoPanic(r, "oversized offline HTML export")
	if r.code != http.StatusRequestEntityTooLarge {
		t.Errorf("expanded widget document returned %d, want a bounded-size 413 response (body length %d)", r.code, len(r.body))
	}
}
