// Package importer reads WordPress WXR exports and imports them into the site
// (docs/tickets/08-system.md, 「WordPress 匯入」).
//
// WXR files are accepted as .xml, .xml.gz or .zip and may be up to 512 MiB.
// Parsing streams the document and holds one item at a time. Attachment
// downloads go through internal/safehttp. Import state lives in import_jobs
// and import_map (migration 0020).
package importer

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxFileBytes is the upload limit for one WXR file.
const MaxFileBytes = 512 << 20

// MaxAttachmentBytes caps one fetched attachment. It matches the media default.
const MaxAttachmentBytes = 20 << 20

// Directories. Uploaded exports stay out of /uploads. Fetched attachments go
// under /uploads/imports so the site serves them like other media.
const (
	ImportsDir = "data/imports"
	UploadsDir = "data/uploads"
)

// Limits bound one parse. They protect against zip bombs and pathological XML.
type Limits struct {
	MaxBytes  int64
	MaxDepth  int
	MaxTokens int64
}

// DefaultLimits are the production limits.
var DefaultLimits = Limits{MaxBytes: MaxFileBytes, MaxDepth: 64, MaxTokens: 10_000_000}

// ErrNotWXR is returned when the input is not a WXR document.
var ErrNotWXR = errors.New("importer: input is not a WXR export")

// ErrLimitExceeded is returned when Limits are hit.
var ErrLimitExceeded = errors.New("importer: input exceeds parser limits")

// ErrTooLarge is returned when an upload is larger than MaxFileBytes.
var ErrTooLarge = errors.New("importer: upload exceeds 512 MiB")

// ErrUnsupportedFile is returned for file names that are not .xml, .xml.gz or .zip.
var ErrUnsupportedFile = errors.New("importer: file must be .xml, .xml.gz or .zip")

// ErrJobNotFound is returned for an unknown import id.
var ErrJobNotFound = errors.New("importer: import not found")

// ErrJobBusy is returned when the import is in a state that does not allow the action.
var ErrJobBusy = errors.New("importer: import is in a state that does not allow this action")

// Item kinds handed to the parse callback.
const (
	ItemPost       = "post"
	ItemPage       = "page"
	ItemAttachment = "attachment"
	ItemAuthor     = "author"
	ItemComment    = "comment"
	// ItemSite carries the channel's wp:base_site_url in Link.
	ItemSite = "site"
)

// Archive kinds returned by Kind.
const (
	archiveXML  = "xml"
	archiveGzip = "gzip"
	archiveZip  = "zip"
)

// Item is one WXR record. Only the fields for its Kind are set.
type Item struct {
	Kind string

	// Posts, pages and attachments.
	WPID          int64
	Title         string
	Slug          string
	Status        string
	Date          string
	Link          string
	Content       string
	Excerpt       string
	AuthorLogin   string
	AttachmentURL string
	PostType      string

	// Authors (channel level).
	Login       string
	Email       string
	DisplayName string

	// Comments. WPID is the comment id, PostWPID the post it belongs to.
	PostWPID       int64
	ParentWPID     int64
	Approved       string
	CommentType    string
	CommenterName  string
	CommenterEmail string
	CommentBody    string
	CommentDate    string
}

// AcceptedName reports whether name has an extension the importer reads.
func AcceptedName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".xml.gz") || strings.HasSuffix(lower, ".zip")
}

// Kind returns the archive kind of name: "xml", "gzip" or "zip".
func Kind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return archiveZip
	case strings.HasSuffix(lower, ".xml.gz"):
		return archiveGzip
	}
	return archiveXML
}

// FilePath is where the stored export of job id lives.
func FilePath(id int64, kind string) string {
	ext := ".xml"
	switch kind {
	case archiveGzip:
		ext = ".xml.gz"
	case archiveZip:
		ext = ".zip"
	}
	return filepath.Join(ImportsDir, idName(id)+ext)
}

func idName(id int64) string {
	return "import-" + strconv.FormatInt(id, 10)
}

// Summary counts the importable items of one export.
type Summary struct {
	Posts       int `json:"posts"`
	Pages       int `json:"pages"`
	Attachments int `json:"attachments"`
	Comments    int `json:"comments"`
	Authors     int `json:"authors"`
}

// Author is one wp:author entry of the export.
type Author struct {
	Login       string `json:"login"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	WPID        int64  `json:"wp_id"`
}

// Issue names one row that needs the admin's attention or was skipped.
type Issue struct {
	Kind   string `json:"kind"`
	Item   string `json:"item"`
	Detail string `json:"detail"`
}

// Report is the outcome of a dry run or a run.
type Report struct {
	Created   map[string]int `json:"created"`
	Skipped   map[string]int `json:"skipped"`
	Conflicts []Issue        `json:"conflicts"`
	Blocked   []Issue        `json:"blocked"`
	Failed    []Issue        `json:"failed"`
}

func newReport() Report {
	return Report{
		Created:   map[string]int{},
		Skipped:   map[string]int{},
		Conflicts: []Issue{},
		Blocked:   []Issue{},
		Failed:    []Issue{},
	}
}

// Mapping is the admin's choice for one import, set before a run.
type Mapping struct {
	Types            []string          `json:"types"`
	FetchAttachments bool              `json:"fetch_attachments"`
	AuthorRoles      map[string]string `json:"author_roles"`
}

// allTypes are the item types an import can include.
var allTypes = []string{"post", "page", "attachment", "comment"}

// ValidType reports whether name is an import type.
func ValidType(name string) bool {
	for _, t := range allTypes {
		if t == name {
			return true
		}
	}
	return false
}

// defaultMapping includes everything and fetches attachments.
func defaultMapping() Mapping {
	return Mapping{Types: append([]string(nil), allTypes...), FetchAttachments: true, AuthorRoles: map[string]string{}}
}

func (m Mapping) includes(typ string) bool {
	for _, t := range m.Types {
		if t == typ {
			return true
		}
	}
	return false
}
