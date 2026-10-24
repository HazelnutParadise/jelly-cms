package theme

import "html/template"

// Content assets (ticket 11, docs/tickets/11-mermaid.md).
//
// ContentAssets returns the <script> tags that the given rendered contents need
// in the page <head>. When any content holds a <code> element whose class list
// contains "language-mermaid", it returns, in this order:
//
//	<script src="/admin/assets/pele/pele.global.js" defer></script>
//	<script src="/admin/assets/jelly-mermaid.js" defer></script>
//	<script>document.addEventListener("DOMContentLoaded",function(){window.JellyMermaid&&window.JellyMermaid.render(document)});</script>
//
// Otherwise it returns "". The tags are emitted once however many diagrams the
// contents hold, and every URL is a same-site path.
//
// TODO(ticket 11): implement; the skeleton returns "".
func ContentAssets(contents ...string) template.HTML {
	return ""
}

// contentAssetsForTemplate backs the "contentAssets" template function, which a
// theme calls as {{contentAssets .}} in its layout <head>. It reads the page
// contents from the template data: the map keys "Post" and "Page" (core.Post or
// *core.Post, their Content field) and "Content" (string or template.HTML). Any
// other key, such as the "Posts" or "Featured" listings, is ignored, and data of
// any other shape yields "".
//
// TODO(ticket 11): implement; the skeleton returns "".
func contentAssetsForTemplate(data any) template.HTML {
	return ""
}
