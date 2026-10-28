package theme

import (
	"html/template"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
)

// ContentAssets returns the same-site assets needed by rendered content. The
// markup package owns feature detection so themes and renderers use one source
// of truth. Scripts are emitted once and in dependency order.
func ContentAssets(contents ...string) template.HTML {
	return contentAssets(contents, func(raw string) string { return raw })
}

func contentAssets(contents []string, assetURL func(string) string) template.HTML {
	features := make(map[string]bool)
	for _, content := range contents {
		for _, feature := range markup.Features(content) {
			features[feature] = true
		}
	}

	needsComponents := features["math"] || features["accordion"] || features["tabs"] ||
		features["carousel"] || features["lightbox"] || features["counter"] ||
		features["countdown"] || features["compare"] || features["widget"]
	needsMath := features["math"]
	needsMotion := features["motion"]
	needsMermaid := features["mermaid"]
	if !needsComponents && !needsMotion && !needsMermaid {
		return ""
	}

	var out template.HTML
	if needsMath {
		out += template.HTML(`<link rel="stylesheet" href="` + template.HTMLEscapeString(assetURL("/vendor/katex/katex.min.css")) + `">`)
		out += template.HTML(`<script src="` + template.HTMLEscapeString(assetURL("/vendor/katex/katex.min.js")) + `" defer></script>`)
	}
	if needsComponents {
		out += template.HTML(`<script src="` + template.HTMLEscapeString(assetURL("/theme/assets/jelly-components.js")) + `" defer></script>`)
	}
	if needsMotion {
		out += template.HTML(`<script src="` + template.HTMLEscapeString(assetURL("/theme/assets/jelly-motion.js")) + `" defer></script>`)
	}
	if needsMermaid {
		out += template.HTML(`<script src="` + template.HTMLEscapeString(assetURL("/admin/assets/pele/pele.global.js")) + `" defer></script>`)
		out += template.HTML(`<script src="` + template.HTMLEscapeString(assetURL("/admin/assets/jelly-mermaid.js")) + `" defer></script>`)
		out += `<script>document.addEventListener("DOMContentLoaded",function(){window.JellyMermaid&&window.JellyMermaid.render(document)});</script>`
	}
	return out
}

// contentAssetsForTemplate backs the "contentAssets" template function. It
// intentionally reads only singular page data, never content listings.
func contentAssetsForTemplate(data any) template.HTML {
	return contentAssetsForTemplateWithURL(data, func(raw string) string { return raw })
}

func contentAssetsForTemplateWithURL(data any, assetURL func(string) string) template.HTML {
	values, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}

	contents := make([]string, 0, 3)
	for _, key := range []string{"Post", "Page"} {
		switch post := values[key].(type) {
		case core.Post:
			contents = append(contents, post.Content)
		case *core.Post:
			if post != nil {
				contents = append(contents, post.Content)
			}
		}
	}
	switch content := values["Content"].(type) {
	case string:
		contents = append(contents, content)
	case template.HTML:
		contents = append(contents, string(content))
	}
	if assetURL == nil {
		assetURL = func(raw string) string { return raw }
	}
	return contentAssets(contents, assetURL)
}
