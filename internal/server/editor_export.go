package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	htmlnode "golang.org/x/net/html"
	"gorm.io/gorm"
)

const maxOfflineExportBytes = 8 << 20

var offlineWidgetNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var errOfflineExportTooLarge = errors.New("standalone export exceeds size limit")

type boundedHTMLWriter struct {
	ctx   context.Context
	limit int
	buf   strings.Builder
}

func (w *boundedHTMLWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.limit-w.buf.Len() {
		return 0, errOfflineExportTooLarge
	}
	return w.buf.Write(p)
}

type offlineWidgetIdentity struct {
	name    string
	version int
}

type offlineWidgetReference struct {
	identity offlineWidgetIdentity
	query    url.Values
	frame    *htmlnode.Node
}

type offlineWidgetCacheKey struct {
	identity offlineWidgetIdentity
	query    string
	lang     string
	builtin  bool
}

// expandOfflineWidgetFrames replaces only server-generated, canonical widget
// frames with srcdoc documents. Registry and version reads are batched.
func expandOfflineWidgetFrames(ctx context.Context, db *gorm.DB, content, lang string) (string, int, error) {
	if len(content) > maxOfflineExportBytes {
		return "", 413, fmt.Errorf("expanded content exceeds %d bytes", maxOfflineExportBytes)
	}
	root, err := htmlnode.Parse(strings.NewReader(content))
	if err != nil {
		return "", 500, err
	}
	refs := make([]offlineWidgetReference, 0)
	namesSet := make(map[string]struct{})
	versionsSet := make(map[int]struct{})
	var walk func(*htmlnode.Node) error
	walk = func(n *htmlnode.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.Type == htmlnode.ElementNode && n.Data == "div" && htmlNodeHasClass(n, "jc-widget") {
			name := htmlNodeAttr(n, "data-widget")
			versionText := htmlNodeAttr(n, "data-v")
			version, parseErr := strconv.Atoi(versionText)
			if offlineWidgetNamePattern.MatchString(name) && parseErr == nil && version > 0 && strconv.Itoa(version) == versionText {
				if frame := findWidgetFrame(n); frame != nil {
					parsed, parseErr := parseCanonicalWidgetFrame(htmlNodeAttr(frame, "src"), name, version)
					if parseErr != nil {
						return parseErr
					}
					if parsed != nil {
						ref := offlineWidgetReference{identity: offlineWidgetIdentity{name: name, version: version}, query: parsed, frame: frame}
						refs = append(refs, ref)
						namesSet[name] = struct{}{}
						versionsSet[version] = struct{}{}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		if ctx.Err() != nil {
			return "", 500, ctx.Err()
		}
		return "", 400, err
	}
	if len(refs) == 0 {
		return content, 200, nil
	}

	names := make([]string, 0, len(namesSet))
	for name := range namesSet {
		names = append(names, name)
	}
	sort.Strings(names)
	var widgets []core.Widget
	if err := db.WithContext(ctx).Unscoped().Where("name IN ?", names).Find(&widgets).Error; err != nil {
		return "", 500, err
	}
	widgetIDs := make([]uint, 0, len(widgets))
	widgetsByName := make(map[string]core.Widget, len(widgets))
	for _, widget := range widgets {
		widgetIDs = append(widgetIDs, widget.ID)
		widgetsByName[widget.Name] = widget
	}
	versions := make([]int, 0, len(versionsSet))
	for version := range versionsSet {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	versionsByIdentity := make(map[struct {
		widgetID uint
		version  int
	}]core.WidgetVersion)
	if len(widgetIDs) > 0 {
		var widgetVersions []core.WidgetVersion
		if err := db.WithContext(ctx).Unscoped().Where("widget_id IN ? AND version IN ? AND published = ?", widgetIDs, versions, true).Find(&widgetVersions).Error; err != nil {
			return "", 500, err
		}
		for _, version := range widgetVersions {
			key := struct {
				widgetID uint
				version  int
			}{version.WidgetID, version.Version}
			versionsByIdentity[key] = version
		}
	}

	documents := make(map[offlineWidgetCacheKey]string)
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return "", 500, err
		}
		var version core.WidgetVersion
		builtin := false
		if widget, ok := widgetsByName[ref.identity.name]; ok {
			key := struct {
				widgetID uint
				version  int
			}{widget.ID, ref.identity.version}
			version, ok = versionsByIdentity[key]
			if !ok {
				if fallback, found := builtinWidgetVersion(ref.identity.name, ref.identity.version); found {
					version, builtin = fallback, true
				} else {
					return "", 400, fmt.Errorf("missing published widget %s version %d", ref.identity.name, ref.identity.version)
				}
			}
		} else if fallback, found := builtinWidgetVersion(ref.identity.name, ref.identity.version); found {
			version, builtin = fallback, true
		} else {
			return "", 400, fmt.Errorf("missing published widget %s version %d", ref.identity.name, ref.identity.version)
		}
		key := offlineWidgetCacheKey{identity: ref.identity, query: ref.query.Encode(), lang: lang, builtin: builtin}
		doc, ok := documents[key]
		if !ok {
			var buildErr error
			doc, buildErr = buildWidgetDocument(version, ref.query, lang, builtin)
			if buildErr != nil {
				return "", 400, buildErr
			}
			if len(doc) > maxOfflineExportBytes {
				return "", 413, fmt.Errorf("widget document exceeds %d bytes", maxOfflineExportBytes)
			}
			documents[key] = doc
		}
		setHTMLNodeAttr(ref.frame, "srcdoc", doc)
		removeHTMLNodeAttr(ref.frame, "src")
	}
	if err := ctx.Err(); err != nil {
		return "", 500, err
	}
	body := findHTMLBody(root)
	if body == nil {
		return "", 500, fmt.Errorf("parsed export has no body")
	}
	output := &boundedHTMLWriter{ctx: ctx, limit: maxOfflineExportBytes}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		if err := htmlnode.Render(output, child); err != nil {
			if errors.Is(err, errOfflineExportTooLarge) {
				return "", 413, err
			}
			return "", 500, err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", 500, err
	}
	return output.buf.String(), 200, nil
}

func findHTMLBody(n *htmlnode.Node) *htmlnode.Node {
	if n.Type == htmlnode.ElementNode && n.Data == "body" {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if body := findHTMLBody(child); body != nil {
			return body
		}
	}
	return nil
}

func parseCanonicalWidgetFrame(raw, name string, version int) (url.Values, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	wantPath := fmt.Sprintf("/widgets/%s/%d/frame", name, version)
	if u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || u.Path != wantPath || u.RawPath != "" {
		return nil, nil
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, err
	}
	return query, nil
}

func htmlNodeHasClass(n *htmlnode.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, value := range strings.Fields(attr.Val) {
				if value == class {
					return true
				}
			}
		}
	}
	return false
}

func htmlNodeAttr(n *htmlnode.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func setHTMLNodeAttr(n *htmlnode.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, htmlnode.Attribute{Key: key, Val: value})
}

func removeHTMLNodeAttr(n *htmlnode.Node, key string) {
	attrs := n.Attr[:0]
	for _, attr := range n.Attr {
		if attr.Key != key {
			attrs = append(attrs, attr)
		}
	}
	n.Attr = attrs
}

func findWidgetFrame(n *htmlnode.Node) *htmlnode.Node {
	if n.Type == htmlnode.ElementNode && n.Data == "iframe" {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findWidgetFrame(child); found != nil {
			return found
		}
	}
	return nil
}

const offlineThemeCSS = `:root{--primary:#E8285B;--secondary:#2F6BFF;--accent:#13C2A3;--warning:#F5B800;--ink:#15161A;--muted:#5F6470;--surface:#FFFFFF;--outline:#DDE0E7;--surface-low:color-mix(in srgb,var(--ink) 3.5%,var(--surface));--surface-mid:color-mix(in srgb,var(--ink) 7%,var(--surface));--color-ink:var(--ink);--color-surface:var(--surface)}`

func buildStandaloneHTMLDocument(ctx context.Context, body, title, lang string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	css, err := os.ReadFile("web/themes/default/assets/content.css")
	if err != nil {
		return "", fmt.Errorf("read content stylesheet: %w", err)
	}
	katexCSS, err := os.ReadFile("web/vendor/katex/katex.min.css")
	if err != nil {
		return "", fmt.Errorf("read math stylesheet: %w", err)
	}
	inlinedKatexCSS, err := inlineKatexFonts(string(katexCSS))
	if err != nil {
		return "", fmt.Errorf("inline math fonts: %w", err)
	}
	features := markup.Features(body)
	featureSet := make(map[string]bool, len(features))
	for _, feature := range features {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		featureSet[feature] = true
	}
	var scripts strings.Builder
	if featureSet["math"] {
		js, err := os.ReadFile("web/vendor/katex/katex.min.js")
		if err != nil {
			return "", fmt.Errorf("read math runtime: %w", err)
		}
		scripts.WriteString("<script>")
		scripts.WriteString(escapeScriptClosingTags(string(js)))
		scripts.WriteString("</script><script>document.querySelectorAll('.jc-math[data-tex]').forEach(function(el){if(el.dataset.mathRendered)return;try{katex.render(el.dataset.tex,el,{displayMode:el.dataset.display==='block',trust:false,throwOnError:false,strict:'warn',maxExpand:1000});el.dataset.mathRendered='true'}catch(_){el.textContent=el.dataset.tex}});</script>")
	}
	if featureSet["accordion"] || featureSet["math"] || featureSet["tabs"] || featureSet["carousel"] || featureSet["lightbox"] || featureSet["counter"] || featureSet["countdown"] || featureSet["compare"] || featureSet["widget"] {
		js, err := os.ReadFile("web/themes/default/assets/jelly-components.js")
		if err != nil {
			return "", fmt.Errorf("read component runtime: %w", err)
		}
		scripts.WriteString("<script>")
		scripts.WriteString(escapeScriptClosingTags(string(js)))
		scripts.WriteString("</script>")
	}
	if featureSet["motion"] {
		js, err := os.ReadFile("web/themes/default/assets/jelly-motion.js")
		if err != nil {
			return "", fmt.Errorf("read motion runtime: %w", err)
		}
		scripts.WriteString("<script>")
		scripts.WriteString(escapeScriptClosingTags(string(js)))
		scripts.WriteString("</script>")
	}
	if featureSet["mermaid"] {
		pele, err := os.ReadFile("web/admin/assets/pele/pele.global.js")
		if err != nil {
			return "", fmt.Errorf("read Pele runtime: %w", err)
		}
		mermaid, err := os.ReadFile("web/admin/assets/jelly-mermaid.js")
		if err != nil {
			return "", fmt.Errorf("read Mermaid runtime: %w", err)
		}
		scripts.WriteString("<script>")
		scripts.WriteString(escapeScriptClosingTags(string(pele)))
		scripts.WriteString("</script><script>")
		scripts.WriteString(escapeScriptClosingTags(string(mermaid)))
		scripts.WriteString("</script><script>document.addEventListener('DOMContentLoaded',function(){if(window.JellyMermaid)window.JellyMermaid.render(document)})</script>")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	doc := fmt.Sprintf("<!doctype html><html lang=\"%s\"><head><meta charset=\"utf-8\"><title>%s</title><style>%s\n%s\n%s</style></head><body><article class=\"prose\">%s</article>%s</body></html>", htmlEscape(lang), htmlEscape(title), offlineThemeCSS, inlinedKatexCSS, css, body, scripts.String())
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(doc) > maxOfflineExportBytes {
		return "", fmt.Errorf("%w: %d bytes", errOfflineExportTooLarge, len(doc))
	}
	return doc, nil
}

var scriptClosingTag = regexp.MustCompile(`(?i)</script`)

func escapeScriptClosingTags(script string) string {
	return scriptClosingTag.ReplaceAllString(script, `<\/script`)
}
