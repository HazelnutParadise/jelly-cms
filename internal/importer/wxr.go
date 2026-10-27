package importer

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Namespaces used by WXR 1.x exports.
const (
	// nsWP is the namespace of the wp: elements in WXR 1.2 exports. Every WXR
	// version starts with nsWPPrefix, which is how a document is recognised.
	nsWP          = "http://wordpress.org/export/1.2/"
	nsWPPrefix    = "http://wordpress.org/export/"
	nsDC          = "http://purl.org/dc/elements/1.1/"
	nsContent     = "http://purl.org/rss/1.0/modules/content/"
	nsExcerpt     = "http://wordpress.org/export/1.2/excerpt/"
	elemItem      = "item"
	elemChannel   = "channel"
	elemWPAuthor  = "author"
	elemWPComment = "comment"
)

// node is one element of an item subtree. Items are small, so each one is
// built in memory while the rest of the file streams past.
type node struct {
	Space string
	Name  string
	Attrs map[string]string
	Text  string
	Kids  []*node
}

func (n *node) child(space, name string) *node {
	for _, k := range n.Kids {
		if k.Name == name && (space == "" || k.Space == space) {
			return k
		}
	}
	return nil
}

func (n *node) text(space, name string) string {
	if k := n.child(space, name); k != nil {
		return strings.TrimSpace(k.Text)
	}
	return ""
}

// limitedReader fails once more than max bytes have been read.
type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, ErrLimitExceeded
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, ErrLimitExceeded
	}
	return n, err
}

// parser wraps the token stream with the limits in Limits.
type parser struct {
	d      *xml.Decoder
	lim    Limits
	tokens int64
	sawWP  bool
}

func (p *parser) next() (xml.Token, error) {
	tok, err := p.d.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if errors.Is(err, ErrLimitExceeded) {
			return nil, ErrLimitExceeded
		}
		return nil, fmt.Errorf("%w: %v", ErrNotWXR, err)
	}
	p.tokens++
	if p.tokens > p.lim.MaxTokens {
		return nil, ErrLimitExceeded
	}
	return tok, nil
}

// readNode consumes the element that start opened, through its end tag.
func (p *parser) readNode(start xml.StartElement, depth int) (*node, error) {
	if depth > p.lim.MaxDepth {
		return nil, ErrLimitExceeded
	}
	n := &node{Space: start.Name.Space, Name: start.Name.Local, Attrs: map[string]string{}}
	for _, a := range start.Attr {
		n.Attrs[a.Name.Local] = a.Value
	}
	var text strings.Builder
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.HasPrefix(t.Name.Space, nsWPPrefix) {
				p.sawWP = true
			}
			kid, err := p.readNode(t, depth+1)
			if err != nil {
				return nil, err
			}
			n.Kids = append(n.Kids, kid)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			n.Text = text.String()
			return n, nil
		}
	}
}

// Parse streams items from r, calling fn for each one in document order.
// Authors come first in a WXR file, so an item's author is already known to
// fn when the item arrives. A document that is not WXR returns ErrNotWXR.
func Parse(ctx context.Context, r io.Reader, lim Limits, fn func(Item) error) error {
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultLimits.MaxBytes
	}
	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultLimits.MaxDepth
	}
	if lim.MaxTokens <= 0 {
		lim.MaxTokens = DefaultLimits.MaxTokens
	}
	src := &limitedReader{r: bufio.NewReader(r), left: lim.MaxBytes}
	dec := xml.NewDecoder(src)
	p := &parser{d: dec, lim: lim}

	depth := 0
	channelSeen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok, err := p.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > lim.MaxDepth {
				return ErrLimitExceeded
			}
			if depth == 1 && t.Name.Local != "rss" {
				return ErrNotWXR
			}
			if strings.HasPrefix(t.Name.Space, nsWPPrefix) {
				p.sawWP = true
			}
			if depth == 2 && t.Name.Local == elemChannel {
				channelSeen = true
				continue
			}
			if depth != 3 || !channelSeen {
				continue
			}
			switch {
			case t.Name.Local == elemItem:
				n, err := p.readNode(t, depth)
				if err != nil {
					return err
				}
				depth--
				if err := emitPost(n, fn); err != nil {
					return err
				}
			case t.Name.Space == nsWP && t.Name.Local == elemWPAuthor:
				n, err := p.readNode(t, depth)
				if err != nil {
					return err
				}
				depth--
				if err := fn(Item{
					Kind:        ItemAuthor,
					Login:       n.text(nsWP, "author_login"),
					Email:       n.text(nsWP, "author_email"),
					DisplayName: n.text(nsWP, "author_display_name"),
					WPID:        parseInt(n.text(nsWP, "author_id")),
				}); err != nil {
					return err
				}
			case t.Name.Space == nsWP && t.Name.Local == "base_site_url":
				n, err := p.readNode(t, depth)
				if err != nil {
					return err
				}
				depth--
				if err := fn(Item{Kind: ItemSite, Link: strings.TrimSpace(n.Text)}); err != nil {
					return err
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	if !p.sawWP || !channelSeen {
		return ErrNotWXR
	}
	return nil
}

// emitPost turns one item subtree into a post, page or attachment item and
// then one item per comment it carries. Other post types are ignored.
func emitPost(n *node, fn func(Item) error) error {
	postType := n.text(nsWP, "post_type")
	var kind string
	switch postType {
	case "post":
		kind = ItemPost
	case "page":
		kind = ItemPage
	case "attachment":
		kind = ItemAttachment
	default:
		return nil
	}
	it := Item{
		Kind:          kind,
		PostType:      postType,
		WPID:          parseInt(n.text(nsWP, "post_id")),
		Title:         strings.TrimSpace(n.text("", "title")),
		Slug:          strings.TrimSpace(n.text(nsWP, "post_name")),
		Status:        n.text(nsWP, "status"),
		Date:          n.text(nsWP, "post_date"),
		Link:          n.text("", "link"),
		Content:       n.text(nsContent, "encoded"),
		Excerpt:       n.text(nsExcerpt, "encoded"),
		AuthorLogin:   n.text(nsDC, "creator"),
		AttachmentURL: n.text(nsWP, "attachment_url"),
	}
	if kind == ItemAttachment && it.AttachmentURL == "" {
		it.AttachmentURL = it.Link
	}
	if err := fn(it); err != nil {
		return err
	}
	for _, k := range n.Kids {
		if k.Space != nsWP || k.Name != elemWPComment {
			continue
		}
		c := Item{
			Kind:           ItemComment,
			WPID:           parseInt(k.text(nsWP, "comment_id")),
			PostWPID:       it.WPID,
			ParentWPID:     parseInt(k.text(nsWP, "comment_parent")),
			CommenterName:  k.text(nsWP, "comment_author"),
			CommenterEmail: k.text(nsWP, "comment_author_email"),
			CommentBody:    k.text(nsWP, "comment_content"),
			CommentDate:    k.text(nsWP, "comment_date"),
			Approved:       k.text(nsWP, "comment_approved"),
			CommentType:    k.text(nsWP, "comment_type"),
		}
		if err := fn(c); err != nil {
			return err
		}
	}
	return nil
}

func parseInt(s string) int64 {
	var v int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		v = v*10 + int64(r-'0')
		if v > 1<<53 {
			return 0
		}
	}
	return v
}

// Source is an open export with its XML stream. Close releases the file and,
// for zip archives, the archive reader.
type Source struct {
	io.Reader
	closers []io.Closer
}

// Close releases everything Open acquired.
func (s *Source) Close() error {
	var first error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OpenSource opens the stored export at path. A zip archive must hold an
// .xml entry, and that entry is read through the same byte limit as a plain file.
func OpenSource(path, kind string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case archiveGzip:
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, ErrNotWXR
		}
		return &Source{Reader: gz, closers: []io.Closer{gz, f}}, nil
	case archiveZip:
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			f.Close()
			return nil, ErrNotWXR
		}
		for _, entry := range zr.File {
			if entry.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name), ".xml") {
				continue
			}
			rc, err := entry.Open()
			if err != nil {
				f.Close()
				return nil, err
			}
			return &Source{Reader: rc, closers: []io.Closer{rc, f}}, nil
		}
		f.Close()
		return nil, ErrNotWXR
	}
	return &Source{Reader: f, closers: []io.Closer{f}}, nil
}
