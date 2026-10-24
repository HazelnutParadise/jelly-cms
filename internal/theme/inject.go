package theme

// InjectBeforeHeadEnd inserts snippet into a rendered page (ticket 13). The
// snippet goes right before the last "</head>" (matched without regard to
// letter case); without one, right before the last "</body>"; without either,
// at the end of the page. The rest of the page is returned byte for byte, and
// the input slice is not modified.
func InjectBeforeHeadEnd(page []byte, snippet string) []byte {
	// TODO(ticket 13): implement the three insertion positions.
	return nil
}

// PlainTextParagraphs returns the visible text of an HTML fragment for the
// WebMCP get_post tool (ticket 13): no tags, entities decoded, script and style
// contents omitted, whitespace inside a block collapsed, and consecutive block
// elements (paragraphs, headings, list items, ...) separated by a blank line.
func PlainTextParagraphs(source string) string {
	// TODO(ticket 13): walk the parsed fragment like PlainText, keeping block breaks.
	return ""
}
