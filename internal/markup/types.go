// Package markup is the single server-side renderer for Jelly MD (ticket 09).
//
// The package owns the source-to-HTML pipeline: GFM, footnotes, heading ids,
// callouts, the ::: container and :: directive syntax, math, YAML tables,
// LaTeX and HTML blocks, and the sanitizer policies that every output passes
// through. Front-end code never parses Markdown. This file holds the public
// types only; behavior is split by concern across parser, policy, math, table,
// and rendering files.
package markup

// Trust levels accepted by Options.Trust and Policy.
const (
	// TrustStrict is the policy for author-written content.
	TrustStrict = "strict"
	// TrustRich is the policy for admin and editor content. It additionally
	// allows the allow-listed elements inside ```html blocks.
	TrustRich = "rich"
)

// Options controls one Render call.
type Options struct {
	// Trust selects the sanitizer policy: TrustStrict or TrustRich.
	Trust string
	// Expand asks Render to leave dynamic blocks (:::products, :::posts) as
	// placeholders; Expand fills them later at request time.
	Expand bool
	// SourceMap adds data-line attributes so the editor can scroll the
	// preview to the source line.
	SourceMap bool
	// Format is "md" for legacy Markdown or "jmd" for Jelly MD.
	Format string
	// Widgets contains the published custom widgets available to this render.
	Widgets map[string]WidgetSpec
}

// Result is the output of Render.
type Result struct {
	// HTML is the sanitized fragment for the post body.
	HTML string
	// TOC lists headings in document order with their ids.
	TOC []TOCEntry
	// Stats holds counts shown in the editor status bar and the post header.
	Stats Stats
	// Features names the front-end features the fragment needs; see Features.
	Features []string
	// Diagnostics are problems found in the source. They never stop a render
	// unless the source is over a size or depth limit.
	Diagnostics []Diagnostic
	// Blocks lists every block in pre-order (parents before children) with source line ranges, used by
	// the layout mode of the editor.
	Blocks []Block
}

// TOCEntry is one heading in the table of contents. It has the same shape as
// the theme package's TOCEntry so the two can be converted without copying.
type TOCEntry struct {
	ID    string
	Text  string
	Level int
}

// Stats holds the counts shown in the editor and on the post header.
type Stats struct {
	Chars     int
	Words     int
	Minutes   int
	Images    int
	Links     int
	OpenTasks int
	Todos     int
}

// Diagnostic is a problem in the source, reported with its position.
type Diagnostic struct {
	Line     int
	Col      int
	Severity string // "error", "warning" or "info"
	Code     string // stable machine-readable code, for example "unknown_attr"
	Message  string
}

// Block describes one top-level block for the layout mode.
type Block struct {
	Kind      string // "container", "directive", "table", "latex", "html", "code" or "paragraph"
	Name      string // for example "hero", "columns" or "embed"
	Attrs     map[string]string
	StartLine int // 1-based, inclusive
	EndLine   int // 1-based, inclusive
}

// DynamicData carries the rows that Expand puts into dynamic blocks. The
// server fills it from the database at request time; markup never queries.
// ItemsByKey carries query-specific rows keyed with DynamicItemsKey. When it is
// non-nil, missing keys intentionally expand to empty lists.
type DynamicData struct {
	Products   []DynamicItem
	Posts      []DynamicItem
	ItemsByKey map[string][]DynamicItem
}

// WidgetSpec describes an immutable published widget version and its declared parameters.
type WidgetSpec struct {
	Version int
	Params  map[string]WidgetParamSpec
}

// WidgetParamSpec constrains one widget query parameter.
type WidgetParamSpec struct {
	Type      string
	Required  bool
	Default   any
	Values    []string
	MaxLength int
}

// DynamicItem is one row of a dynamic list.
type DynamicItem struct {
	Title string
	URL   string
}

// SchemaDoc describes the syntax for GET /api/admin/render/schema: the blocks,
// their attributes and enumerations, and the slash-menu items.
type SchemaDoc struct {
	Blocks []BlockSpec
	Slash  []SlashItem
}

// BlockSpec describes one block name and its allowed attributes.
type BlockSpec struct {
	Name  string
	Kind  string              // "container", "directive" or "line"
	Attrs map[string][]string // attribute name → allowed values; nil when free-form
}

// SlashItem is one entry of the editor's slash menu.
type SlashItem struct {
	Key    string
	Label  string
	Insert string
}
