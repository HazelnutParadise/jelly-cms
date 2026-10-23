// Package importer reads WordPress WXR exports and streams their items.
//
// WXR files are accepted as .xml, .xml.gz or .zip and may be up to 512 MiB.
// Parsing is streaming and bounded by Limits; attachment downloads go through
// internal/safehttp.
package importer

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// MaxFileBytes is the upload limit for one WXR file.
const MaxFileBytes = 512 << 20

// Limits bound one parse. They protect against zip bombs and pathological XML.
type Limits struct {
	MaxBytes  int64
	MaxDepth  int
	MaxTokens int64
}

// DefaultLimits are the production limits.
var DefaultLimits = Limits{MaxBytes: MaxFileBytes, MaxDepth: 64, MaxTokens: 10_000_000}

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("importer: not implemented")

// ErrNotWXR is returned when the input is not a WXR document.
var ErrNotWXR = errors.New("importer: input is not a WXR export")

// ErrLimitExceeded is returned when Limits are hit.
var ErrLimitExceeded = errors.New("importer: input exceeds parser limits")

// Item is one WXR record handed to the callback. Kind is post, page,
// attachment, term, author or comment.
type Item struct {
	Kind        string
	WPID        int64
	Title       string
	Slug        string
	Status      string
	Date        string
	Link        string
	Content     string
	AuthorLogin string
	Categories  []string
	Tags        []string
	Meta        map[string]string
}

// AcceptedName reports whether name has an extension the importer reads.
func AcceptedName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".xml.gz") || strings.HasSuffix(lower, ".zip")
}

// Kind returns the archive kind of name: "xml", "gzip" or "zip".
func Kind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip":
		return "zip"
	case ".gz":
		return "gzip"
	}
	return "xml"
}

// Parse streams items from r, calling fn for each one in document order.
// TODO(08c): implement with encoding/xml token streaming.
func Parse(ctx context.Context, r io.Reader, lim Limits, fn func(Item) error) error {
	return ErrNotImplemented
}
