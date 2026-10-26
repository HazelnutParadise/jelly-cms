package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

// exportExtensions maps each format to the file extension its export is saved under.
var exportExtensions = map[string]string{
	FormatMarkdownZip: ".zip",
	FormatWXR:         ".xml",
	FormatJSON:        ".json",
}

var unsafeSlugChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Export writes posts and pages in format under ExportDirectory and returns the
// export ID. Exports hold content only, never users' credentials or settings.
func Export(ctx context.Context, format string) (int64, error) {
	ext, ok := exportExtensions[format]
	if !ok {
		return 0, ErrUnknownFormat
	}
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if err := prepareDir(ExportDirectory); err != nil {
		return 0, err
	}
	var posts []core.Post
	if err := data.DB.WithContext(ctx).Order("id").Find(&posts).Error; err != nil {
		return 0, fmt.Errorf("backup: read posts: %w", err)
	}

	id, file, err := createExportFile(ext)
	if err != nil {
		return 0, err
	}
	var werr error
	switch format {
	case FormatMarkdownZip:
		werr = writeMarkdownZip(file, posts)
	case FormatWXR:
		werr = writeWXR(ctx, file, posts)
	case FormatJSON:
		werr = writeJSONExport(file, posts)
	}
	if cerr := file.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(file.Name())
		return 0, werr
	}
	return id, nil
}

// createExportFile reserves a random export ID and creates its file with
// O_EXCL, so two exports never share a name.
func createExportFile(ext string) (int64, *os.File, error) {
	for attempt := 0; attempt < 8; attempt++ {
		raw, err := randomBytes(8)
		if err != nil {
			return 0, nil, err
		}
		id := int64(binary.BigEndian.Uint64(raw) & (1<<53 - 1))
		if id == 0 {
			continue
		}
		f, err := os.OpenFile(filepath.Join(ExportDirectory, fmt.Sprintf("%d%s", id, ext)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return 0, nil, fmt.Errorf("backup: create export: %w", err)
		}
		return id, f, nil
	}
	return 0, nil, errors.New("backup: could not reserve an export ID")
}

// ExportPath returns the file of export id and the name to download it under.
func ExportPath(id int64) (string, string, error) {
	matches, err := filepath.Glob(filepath.Join(ExportDirectory, fmt.Sprintf("%d.*", id)))
	if err != nil || len(matches) != 1 {
		return "", "", ErrNotFound
	}
	return matches[0], fmt.Sprintf("jelly-export-%d%s", id, filepath.Ext(matches[0])), nil
}

// writeMarkdownZip writes one .md file per post and per page, with YAML front
// matter. Source written in Markdown or jmd is flattened by markup.Flatten.
func writeMarkdownZip(w io.Writer, posts []core.Post) error {
	zw := zip.NewWriter(w)
	seen := map[string]bool{}
	for i := range posts {
		p := &posts[i]
		dir := "posts"
		if p.Type == "page" {
			dir = "pages"
		}
		name := dir + "/" + markdownFileName(p, seen)
		entry, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(entry, frontMatter(p)+markdownBody(p)); err != nil {
			return err
		}
	}
	return zw.Close()
}

// markdownFileName builds a file name from the slug. A name that is already
// taken gets the post ID appended.
func markdownFileName(p *core.Post, seen map[string]bool) string {
	base := strings.Trim(unsafeSlugChars.ReplaceAllString(p.Slug, "-"), "-.")
	if base == "" {
		base = fmt.Sprintf("item-%d", p.ID)
	}
	name := base + ".md"
	if seen[name] {
		name = fmt.Sprintf("%s-%d.md", base, p.ID)
	}
	seen[name] = true
	return name
}

func frontMatter(p *core.Post) string {
	return fmt.Sprintf("---\ntitle: %s\nslug: %s\ntype: %s\nstatus: %s\ndate: %s\n---\n\n",
		quoteYAML(p.Title), quoteYAML(p.Slug), quoteYAML(p.Type), quoteYAML(p.Status),
		quoteYAML(p.CreatedAt.UTC().Format(time.RFC3339)))
}

// quoteYAML returns s as a double-quoted YAML scalar. JSON string syntax is a
// valid YAML double-quoted scalar.
func quoteYAML(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// markdownBody returns the post body as Markdown. Markdown and jmd sources are
// flattened, HTML bodies are kept as they are.
func markdownBody(p *core.Post) string {
	if (p.Format == "md" || p.Format == "jmd") && strings.TrimSpace(p.Source) != "" {
		if flat, err := markup.Flatten(p.Source); err == nil {
			return flat
		}
	}
	return p.Content
}

// wxrItem is one WordPress eXtended RSS item.
type wxrItem struct {
	XMLName  xml.Name `xml:"item"`
	Title    string   `xml:"title"`
	Link     string   `xml:"link"`
	PubDate  string   `xml:"pubDate"`
	Creator  string   `xml:"dc:creator"`
	Content  string   `xml:"content:encoded"`
	Excerpt  string   `xml:"excerpt:encoded"`
	PostID   uint     `xml:"wp:post_id"`
	PostDate string   `xml:"wp:post_date"`
	PostName string   `xml:"wp:post_name"`
	Status   string   `xml:"wp:status"`
	PostType string   `xml:"wp:post_type"`
}

// writeWXR writes a WXR 1.2 file that WordPress importers and this CMS's own
// importer (ticket 08c) read.
func writeWXR(ctx context.Context, w io.Writer, posts []core.Post) error {
	var users []core.User
	if err := data.DB.WithContext(ctx).Select("id", "username").Find(&users).Error; err != nil {
		return fmt.Errorf("backup: read authors: %w", err)
	}
	names := make(map[uint]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Username
	}

	var head bytes.Buffer
	head.WriteString(xml.Header)
	head.WriteString(`<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/"><channel><title>`)
	if err := xml.EscapeText(&head, []byte(settings.Get(ctx, "site_title"))); err != nil {
		return err
	}
	head.WriteString(`</title><wp:wxr_version>1.2</wp:wxr_version>`)
	if _, err := w.Write(head.Bytes()); err != nil {
		return err
	}

	enc := xml.NewEncoder(w)
	for i := range posts {
		p := &posts[i]
		link := "/post/" + p.Slug
		if p.Type == "page" {
			link = "/" + p.Slug
		}
		item := wxrItem{
			Title:    p.Title,
			Link:     link,
			PubDate:  p.CreatedAt.UTC().Format(time.RFC1123Z),
			Creator:  names[p.AuthorID],
			Content:  p.Content,
			Excerpt:  p.Excerpt,
			PostID:   p.ID,
			PostDate: p.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
			PostName: p.Slug,
			Status:   wxrStatus(p.Status),
			PostType: p.Type,
		}
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</channel></rss>\n")
	return err
}

func wxrStatus(status string) string {
	switch status {
	case "published":
		return "publish"
	case "":
		return "draft"
	}
	return status
}

// exportItem is one post or page in the JSON export.
type exportItem struct {
	ID          uint       `json:"id"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Status      string     `json:"status"`
	AuthorID    uint       `json:"author_id"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"`
	Source      string     `json:"source,omitempty"`
	Format      string     `json:"format,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func writeJSONExport(w io.Writer, posts []core.Post) error {
	items := make([]exportItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, exportItem{
			ID: p.ID, Type: p.Type, Title: p.Title, Slug: p.Slug, Status: p.Status,
			AuthorID: p.AuthorID, Excerpt: p.Excerpt, Content: p.Content,
			Source: p.Source, Format: p.Format, PublishedAt: p.PublishedAt,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	doc := map[string]any{
		"format":      "jelly-cms-export",
		"version":     1,
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"items":       items,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
