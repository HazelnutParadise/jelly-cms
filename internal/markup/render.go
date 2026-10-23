package markup

import (
	"context"
	"html"
)

// Render turns source into sanitized HTML and the metadata the editor and the
// post page need (TOC, stats, features, diagnostics, layout blocks).
//
// Contract (docs/tickets/09-editor.md, 語法表): GFM, footnotes, heading ids
// that are idempotent, > [!NOTE] callouts, <!--more-->, ::: containers,
// ::name{} directives, {#id .cls key=value} attributes, $…$ and $$…$$ math,
// ```table YAML tables, ```latex, ```html and highlighted code. The output is
// always passed through Policy(opt.Trust). Render never writes to the database
// and never fetches remote resources. It returns an error only for a source
// over the size limit or a render over the time limit; syntax problems become
// Diagnostics.
//
// Stub: the source is HTML-escaped and wrapped in one paragraph, with no
// diagnostics, so existing pages keep working until G0 lands.
func Render(ctx context.Context, source string, opt Options) (Result, error) {
	_ = ctx
	_ = opt
	return Result{HTML: "<p>" + html.EscapeString(source) + "</p>"}, nil
}

// Flatten converts Jelly MD to plain Markdown for the md export. It follows the
// AST line ranges: fields become paragraphs, products become a link list,
// table becomes a pipe table, callouts become "> **Note**", tabs and faq become
// headings, attributes and motion are removed, and components and embeds become
// links. html, latex blocks and $…$ math are kept as fenced or inline source.
//
// Stub: returns source unchanged.
func Flatten(source string) (string, error) {
	return source, nil
}

// Expand fills the dynamic placeholders of an already rendered fragment
// (:::products and :::posts) with the rows in data. It runs per request, so
// the stored HTML never contains live data.
//
// Stub: returns html unchanged.
func Expand(ctx context.Context, html string, data DynamicData) (string, error) {
	_ = ctx
	_ = data
	return html, nil
}

// Features reports which front-end features a rendered fragment needs, so the
// theme loads jelly-motion.js, jelly-components.js or the KaTeX CSS only when
// the page uses them. Values are "math", "motion", "tabs", "carousel",
// "lightbox", "widget" and similar names.
//
// Stub: returns nil, meaning no optional assets are loaded.
func Features(html string) []string {
	_ = html
	return nil
}

// Schema describes the syntax for the editor: blocks, attributes with their
// enumerations, and the slash-menu items. It backs
// GET /api/admin/render/schema.
//
// Stub: returns an empty document.
func Schema() SchemaDoc {
	return SchemaDoc{}
}
