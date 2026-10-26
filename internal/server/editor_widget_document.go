package server

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/url"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
)

const widgetFrameCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'"
const widgetDocumentCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'"

var (
	errWidgetDocumentDefinition = errors.New("invalid widget definition")
	errWidgetDocumentParams     = errors.New("invalid widget parameters")
)

const widgetHeightBootstrap = `<script>window.parent.postMessage({type:"jelly-widget-height",height:document.documentElement.scrollHeight},"*");new ResizeObserver(()=>window.parent.postMessage({type:"jelly-widget-height",height:document.documentElement.scrollHeight},"*")).observe(document.documentElement);</script>`

// buildWidgetDocument builds the same opaque child document for the live frame
// endpoint and srcdoc exports. The source is the immutable, published version.
func buildWidgetDocument(ver core.WidgetVersion, query url.Values, lang string, builtin bool) (string, error) {
	var params []widgetParam
	if err := json.Unmarshal([]byte(ver.Params), &params); err != nil {
		return "", fmt.Errorf("%w: %v", errWidgetDocumentDefinition, err)
	}
	spec := markup.WidgetSpec{Version: ver.Version, Params: make(map[string]markup.WidgetParamSpec, len(params))}
	for _, p := range params {
		spec.Params[p.Name] = markup.WidgetParamSpec{Type: p.Type, Required: p.Required, Default: p.Default, Values: p.Values, MaxLength: p.MaxLength}
	}
	provided := make(map[string]string, len(query))
	for name, values := range query {
		if _, declared := spec.Params[name]; declared && len(values) > 0 {
			provided[name] = values[0]
		}
	}
	values, err := markup.ValidateWidgetParams(spec, provided)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errWidgetDocumentParams, err)
	}
	props, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode widget properties: %w", err)
	}
	source := ver.Source
	if builtin {
		to, _ := values["to"].(string)
		source = strings.ReplaceAll(source, "__JELLY_COUNTDOWN_TO__", stdhtml.EscapeString(to))
		if lang == "en" || strings.HasPrefix(lang, "en-") {
			source = strings.NewReplacer(
				"__JELLY_COUNTDOWN_DONE__", "Deadline passed",
				"__JELLY_COUNTDOWN_REMAINING__", "Remaining ",
				"__JELLY_COUNTDOWN_DAYS__", " days ",
				"__JELLY_COUNTDOWN_HOURS__", " hours ",
				"__JELLY_COUNTDOWN_MINUTES__", " minutes",
			).Replace(source)
		} else {
			source = strings.NewReplacer(
				"__JELLY_COUNTDOWN_DONE__", "已截止",
				"__JELLY_COUNTDOWN_REMAINING__", "剩餘 ",
				"__JELLY_COUNTDOWN_DAYS__", " 天 ",
				"__JELLY_COUNTDOWN_HOURS__", " 小時 ",
				"__JELLY_COUNTDOWN_MINUTES__", " 分鐘",
			).Replace(source)
		}
	}
	doc := "<!doctype html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"Content-Security-Policy\" content=\"" + widgetDocumentCSP + "\"></head><body>" + widgetThemeBootstrap + "<script>const props=" + string(props) + ";</script>" + source + widgetHeightBootstrap + "</body></html>"
	return doc, nil
}
