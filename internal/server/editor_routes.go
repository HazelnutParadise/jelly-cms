package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/labstack/echo/v4"
	htmlnode "golang.org/x/net/html"
	"gorm.io/gorm"
)

// Editor API (ticket 09). JSON in and out; errors are {"error": "..."}.
//
//	POST   /api/admin/render                          admin/editor/author {source, post_id?, mode: fragment|page, sourcemap, expand}
//	                                                  → {html, toc, stats, features, diagnostics, blocks, renderer}; never writes; 2 MB; per-user rate limit
//	GET    /api/admin/render/schema                   admin/editor/author → blocks, attributes, enumerations, slash-menu items
//	POST   /api/admin/posts/:id/autosave              owner or editor {title, source, excerpt} → writes an autosave revision only
//	GET    /api/admin/posts/:id/export?format=jmd|md|html|pdf&images=1&frontmatter=1&history=1
//	                                                  owner or editor; 404 for another author's post, 400 bad format, 501 pdf_engine_unavailable
//	GET    /api/admin/widgets                         admin → custom widget registry, drafts and published versions
//	POST   /api/admin/widgets                         admin → create a custom widget (HTML, CSS, JS, parameter declarations)
//	PUT    /api/admin/widgets/:id                     admin → update a draft; runs the automatic publish checks
//	DELETE /api/admin/widgets/:id                     admin
//	POST   /api/admin/widgets/:id/publish             admin → runs the publish checks, then publishes a version
//	GET    /widgets/:name/:v/frame?…                  public → sandboxed widget frame with the CSP from the ticket; parameters checked against the declaration
//
// Role rules: render and schema use the content group (admin, editor, author);
// autosave and export use the content group too and check ownership inside the
// handler; widgets use the admin-only group; the frame is public.

// registerEditorRoutes registers the editor endpoints. content is the group
// for admin, editor and author; api is the admin-only group.
func registerEditorRoutes(e *echo.Echo, content, api *echo.Group) {
	limiter := newRenderLimiter()
	content.POST("/render", func(c echo.Context) error { return renderEditorSource(c, limiter) })
	content.GET("/render/schema", func(c echo.Context) error { return c.JSON(http.StatusOK, markup.Schema()) })
	content.POST("/posts/:id/autosave", autosavePost)
	content.GET("/posts/:id/export", exportPost)

	// Widget audit wraps authorization so failed authentication and role checks
	// are recorded alongside successful management requests.
	e.Add(http.MethodGet, "/api/admin/widgets", widgetList, widgetRequestAudit("list"), auth.RequireRole("admin"))
	e.Add(http.MethodPost, "/api/admin/widgets", createWidget, widgetRequestAudit("create"), auth.RequireRole("admin"))
	e.Add(http.MethodPut, "/api/admin/widgets/:id", updateWidget, widgetRequestAudit("update"), auth.RequireRole("admin"))
	e.Add(http.MethodDelete, "/api/admin/widgets/:id", deleteWidget, widgetRequestAudit("delete"), auth.RequireRole("admin"))
	e.Add(http.MethodPost, "/api/admin/widgets/:id/publish", publishWidget, widgetRequestAudit("publish"), auth.RequireRole("admin"))

	e.GET("/widgets/:name/:v/frame", widgetFrame)
}

type renderLimiter struct {
	mu   sync.Mutex
	hits map[uint][]time.Time
}

func newRenderLimiter() *renderLimiter { return &renderLimiter{hits: map[uint][]time.Time{}} }
func (l *renderLimiter) allow(userID uint, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, timestamps := range l.hits {
		recent := timestamps[:0]
		for _, at := range timestamps {
			if now.Sub(at) < time.Minute {
				recent = append(recent, at)
			}
		}
		if len(recent) == 0 {
			delete(l.hits, id)
		} else {
			l.hits[id] = recent
		}
	}
	items := l.hits[userID]
	if len(items) >= 60 {
		return false
	}
	if len(items) == 0 && len(l.hits) >= 4096 {
		return false
	}
	l.hits[userID] = append(items, now)
	return true
}

func renderEditorSource(c echo.Context, limiter *renderLimiter) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 3<<20)
	var req struct {
		Source    string `json:"source"`
		PostID    uint   `json:"post_id"`
		Mode      string `json:"mode"`
		SourceMap bool   `json:"sourcemap"`
		Expand    bool   `json:"expand"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "source exceeds 2 MB"})
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if len(req.Source) > 2<<20 {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "source exceeds 2 MB"})
	}
	if req.Mode != "" && req.Mode != "fragment" && req.Mode != "page" {
		return c.JSON(400, map[string]string{"error": "invalid mode"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	if req.PostID != 0 {
		var post core.Post
		if err := visiblePostQuery(data.DB.WithContext(ctx), user, req.PostID).First(&post).Error; err != nil {
			return postQueryError(c, err)
		}
	}
	if !limiter.allow(user.ID, time.Now()) {
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "render rate limit exceeded"})
	}
	trust := renderTrustFor(user)
	db := data.DB.WithContext(ctx)
	widgets, err := publishedWidgetSpecs(db)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "unable to load published widgets"})
	}
	result, err := markup.Render(ctx, req.Source, markup.Options{Trust: trust, Expand: req.Expand, SourceMap: req.SourceMap, Format: "jmd", Widgets: widgets})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error(), "diagnostics": []markup.Diagnostic{}})
	}
	for _, d := range result.Diagnostics {
		if d.Severity == "error" {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "render diagnostics", "diagnostics": result.Diagnostics})
		}
	}
	if req.Expand {
		result.HTML, err = expandPublicBlocks(ctx, db, result.HTML)
		if err != nil {
			return c.JSON(500, map[string]string{"error": "unable to expand dynamic content"})
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"html": result.HTML, "toc": result.TOC, "stats": result.Stats, "features": result.Features, "diagnostics": result.Diagnostics, "blocks": result.Blocks, "renderer": 1})
}

func autosavePost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parsePostID(c.Param("id"))
	if !ok {
		return c.JSON(404, map[string]string{"error": "Post not found"})
	}
	var post core.Post
	if err := visiblePostQuery(data.DB.WithContext(c.Request().Context()), user, id).First(&post).Error; err != nil {
		return postQueryError(c, err)
	}
	var req struct {
		Title   string `json:"title"`
		Source  string `json:"source"`
		Excerpt string `json:"excerpt"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request"})
	}
	if len(req.Source) > 2<<20 {
		return c.JSON(413, map[string]string{"error": "source exceeds 2 MB"})
	}
	post.Title, post.Source, post.Excerpt = req.Title, req.Source, req.Excerpt
	post.Format = "jmd"
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error { _, e := RecordRevision(tx, post, user.ID, "autosave"); return e })
	if err != nil {
		return c.JSON(500, map[string]string{"error": "Unable to autosave"})
	}
	return c.JSON(200, map[string]string{"status": "saved"})
}

func exportPost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parsePostID(c.Param("id"))
	if !ok {
		return c.JSON(404, map[string]string{"error": "Post not found"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	var p core.Post
	db := data.DB.WithContext(ctx)
	if err := visiblePostQuery(db, user, id).First(&p).Error; err != nil {
		return postQueryError(c, err)
	}
	format := strings.ToLower(c.QueryParam("format"))
	if format == "pdf" {
		return c.JSON(501, map[string]string{"error": "pdf_engine_unavailable"})
	}
	if format != "jmd" && format != "md" && format != "html" {
		return c.JSON(400, map[string]string{"error": "unsupported format"})
	}
	source := p.Source
	if p.Format == "html" {
		source = p.Content
	}
	if format == "jmd" {
		if c.QueryParam("frontmatter") == "1" {
			source = fmt.Sprintf("---\ntitle: %q\nslug: %q\n---\n%s", p.Title, p.Slug, source)
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
		return c.String(200, source)
	}
	if format == "md" {
		source, e := expandDynamicSourceForMarkdown(ctx, db, source, p.Format)
		if e != nil {
			return c.JSON(500, map[string]string{"error": "export dynamic content unavailable"})
		}
		flat, e := markup.Flatten(source)
		if e != nil {
			return c.JSON(400, map[string]string{"error": e.Error()})
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
		return c.String(200, flat)
	}
	body := p.Content
	if p.RenderVersion < 1 {
		body = markup.Policy(markup.TrustStrict).Sanitize(body)
	}
	body, err = expandPublicBlocks(ctx, db, body)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "export dynamic content unavailable"})
	}
	css, cssErr := os.ReadFile("web/themes/default/assets/content.css")
	if cssErr != nil {
		return c.JSON(500, map[string]string{"error": "export content stylesheet unavailable"})
	}
	katex, katexErr := os.ReadFile("web/vendor/katex/katex.min.css")
	if katexErr != nil {
		return c.JSON(500, map[string]string{"error": "export math stylesheet unavailable"})
	}
	katexCSS, e := inlineKatexFonts(string(katex))
	if e != nil {
		return c.JSON(500, map[string]string{"error": "export math fonts unavailable"})
	}
	lang := user.Locale
	if strings.TrimSpace(lang) == "" {
		lang = "zh-TW"
	}
	mathScript := ""
	if strings.Contains(body, `class="jc-math"`) {
		js, e := os.ReadFile("web/vendor/katex/katex.min.js")
		if e != nil {
			return c.JSON(500, map[string]string{"error": "export math engine unavailable"})
		}
		safeJS := strings.ReplaceAll(string(js), "</script", "<\\/script")
		mathScript = "<script>" + safeJS + "</script><script>document.querySelectorAll('.jc-math[data-tex]').forEach(function(el){katex.render(el.dataset.tex,el,{trust:false,throwOnError:false,displayMode:el.tagName==='DIV'})})</script>"
	}
	htmlDoc := fmt.Sprintf("<!doctype html><html lang=\"%s\"><head><meta charset=\"utf-8\"><title>%s</title><style>%s\n%s</style></head><body><article>%s</article>%s</body></html>", htmlEscape(lang), htmlEscape(p.Title), katexCSS, css, body, mathScript)
	if c.QueryParam("images") != "1" {
		c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
		return c.String(200, htmlDoc)
	}
	zipHTML, paths, e := rewriteUploadAttrs(htmlDoc)
	if e != nil {
		return c.JSON(400, map[string]string{"error": "invalid upload path"})
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, e := zw.Create("index.html")
	if e != nil {
		return c.JSON(500, map[string]string{"error": "zip failed"})
	}
	if _, e = f.Write([]byte(zipHTML)); e != nil {
		return c.JSON(500, map[string]string{"error": "zip failed"})
	}
	for _, rel := range paths {
		dataBytes, e := os.ReadFile(filepath.Join("data/uploads", filepath.FromSlash(rel)))
		if e != nil {
			return c.JSON(500, map[string]string{"error": "export image unavailable"})
		}
		f, e := zw.Create(filepath.ToSlash(filepath.Join("images", filepath.FromSlash(rel))))
		if e != nil {
			return c.JSON(500, map[string]string{"error": "zip failed"})
		}
		if _, e = f.Write(dataBytes); e != nil {
			return c.JSON(500, map[string]string{"error": "zip failed"})
		}
	}
	if err := zw.Close(); err != nil {
		return c.JSON(500, map[string]string{"error": "zip failed"})
	}
	c.Response().Header().Set(echo.HeaderContentType, "application/zip")
	c.Response().Header().Set(echo.HeaderContentDisposition, "attachment; filename=\"post.zip\"")
	return c.Blob(200, "application/zip", buf.Bytes())
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(s)
}

func expandDynamicSourceForMarkdown(ctx context.Context, db *gorm.DB, source, format string) (string, error) {
	if format != "md" {
		format = "jmd"
	}
	rendered, err := markup.Render(ctx, source, markup.Options{Trust: markup.TrustStrict, Format: format})
	if err != nil {
		return "", err
	}
	lines := strings.Split(source, "\n")
	blocks := append([]markup.Block(nil), rendered.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].StartLine < blocks[j].StartLine })
	outer := make([]markup.Block, 0, len(blocks))
	for _, b := range blocks {
		if b.Name != "products" && b.Name != "posts" {
			continue
		}
		nested := false
		for _, parent := range outer {
			if b.StartLine > parent.StartLine && b.EndLine <= parent.EndLine {
				nested = true
				break
			}
		}
		if !nested {
			outer = append(outer, b)
		}
	}
	for i := len(outer) - 1; i >= 0; i-- {
		b := outer[i]
		items, e := queryDynamicItems(db, b.Name, b.Attrs)
		if e != nil {
			return "", e
		}
		var replacement []string
		for _, item := range items {
			title := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]").Replace(item.Title)
			url := strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(item.URL)
			replacement = append(replacement, "- ["+title+"]("+url+")")
		}
		start, end := b.StartLine-1, b.EndLine
		if start < 0 || end > len(lines) || start >= end {
			continue
		}
		next := append([]string{}, lines[:start]...)
		next = append(next, replacement...)
		next = append(next, lines[end:]...)
		lines = next
	}
	return strings.Join(lines, "\n"), nil
}
func inlineKatexFonts(css string) (string, error) {
	re := regexp.MustCompile(`url\(["']?fonts/(KaTeX_[A-Za-z0-9_-]+)\.(?:woff2|woff|ttf)["']?\)`)
	var failed error
	out := re.ReplaceAllStringFunc(css, func(ref string) string {
		m := re.FindStringSubmatch(ref)
		b, e := os.ReadFile(filepath.Join("web/vendor/katex/fonts", m[1]+".woff2"))
		if e != nil {
			failed = e
			return ref
		}
		return `url("data:font/woff2;base64,` + base64.StdEncoding.EncodeToString(b) + `")`
	})
	return out, failed
}
func rewriteUploadAttrs(doc string) (string, []string, error) {
	root, e := htmlnode.Parse(strings.NewReader(doc))
	if e != nil {
		return "", nil, e
	}
	paths := []string{}
	seen := map[string]bool{}
	rewrite := func(raw string) (string, error) {
		if !strings.HasPrefix(raw, "/uploads/") {
			return raw, nil
		}
		rel := strings.TrimPrefix(raw, "/uploads/")
		clean := filepath.Clean(filepath.FromSlash(rel))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return "", fmt.Errorf("unsafe upload path")
		}
		rel = filepath.ToSlash(clean)
		if !seen[rel] {
			seen[rel] = true
			paths = append(paths, rel)
		}
		return "images/" + rel, nil
	}
	var walk func(*htmlnode.Node) error
	walk = func(n *htmlnode.Node) error {
		if n.Type == htmlnode.ElementNode {
			for i := range n.Attr {
				a := &n.Attr[i]
				switch strings.ToLower(a.Key) {
				case "src", "href", "poster":
					v, err := rewrite(a.Val)
					if err != nil {
						return err
					}
					a.Val = v
				case "srcset":
					candidates := strings.Split(a.Val, ",")
					for i, candidate := range candidates {
						fields := strings.Fields(strings.TrimSpace(candidate))
						if len(fields) == 0 {
							continue
						}
						v, err := rewrite(fields[0])
						if err != nil {
							return err
						}
						fields[0] = v
						candidates[i] = strings.Join(fields, " ")
					}
					a.Val = strings.Join(candidates, ", ")
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	if e := walk(root); e != nil {
		return "", nil, e
	}
	var b strings.Builder
	if e := htmlnode.Render(&b, root); e != nil {
		return "", nil, e
	}
	return b.String(), paths, nil
}

// renderTrustFor returns the sanitizer policy for a user's content. Admins and
// editors may use the rich policy (allow-listed html blocks); every other role,
// including author, gets strict. A nil user gets strict.
func renderTrustFor(user *core.User) string {
	if user != nil && (user.Role == "admin" || user.Role == "editor") {
		return markup.TrustRich
	}
	return markup.TrustStrict
}
