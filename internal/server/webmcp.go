package server

// WebMCP for public theme pages (ticket 13, docs/tickets/13-webmcp.md).
//
// GET /_jelly/webmcp.js          the compatibility layer and registration script (web/jelly/webmcp.js)
// GET /_jelly/webmcp-tools.json  the merged tool list: core, loaded plugins, then the active theme
//
// Both answer 404 while the option webmcp_enabled is "false". Theme pages get
// webmcpScriptTag inserted with theme.InjectBeforeHeadEnd while WebMCP is on,
// and carry "Permissions-Policy: tools=()" while it is off.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const (
	// webmcpEnabledKey is the option row that switches WebMCP; a missing row means "true".
	webmcpEnabledKey = "webmcp_enabled"
	// webmcpScriptTag is inserted once into every public theme page while WebMCP is on.
	webmcpScriptTag = `<script type="module" src="/_jelly/webmcp.js"></script>`
	// webmcpScriptFile is the vendored script served at /_jelly/webmcp.js.
	webmcpScriptFile = "web/jelly/webmcp.js"
)

var webMCPToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func registerWebMCPRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	e.GET("/_jelly/webmcp.js", func(c echo.Context) error {
		if !webMCPEnabled() {
			return echo.ErrNotFound
		}
		body, err := os.ReadFile(webmcpScriptFile)
		if err != nil {
			log.Printf("read WebMCP script: %v", err)
			return echo.ErrNotFound
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/javascript; charset=utf-8")
		return c.Blob(http.StatusOK, "text/javascript; charset=utf-8", body)
	})
	e.GET("/_jelly/webmcp-tools.json", func(c echo.Context) error {
		if !webMCPEnabled() {
			return echo.ErrNotFound
		}
		return c.JSON(http.StatusOK, map[string]any{"tools": webMCPToolRegistry(tm, pluginRuntime)})
	})
}

func webMCPToolRegistry(tm *theme.Manager, runtime *plugin.Runtime) []map[string]any {
	tools := webMCPBuiltinTools()
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		seen[tool["name"].(string)] = struct{}{}
	}
	if runtime != nil {
		for _, metadata := range runtime.LoadedPlugins() {
			tools = appendWebMCPDeclarations(tools, seen, metadata.WebMCPTools, "plugin:"+metadata.ID)
		}
	}
	if tm != nil {
		if name := tm.GetActive(); name != "" {
			config, err := tm.Load(name)
			if err != nil {
				log.Printf("webmcp: cannot load active theme %q declarations: %v", name, err)
			} else {
				tools = appendWebMCPDeclarations(tools, seen, config.WebMCPTools, "theme:"+name)
			}
		}
	}
	return tools
}

func appendWebMCPDeclarations(tools []map[string]any, seen map[string]struct{}, raw json.RawMessage, source string) []map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return tools
	}
	var declarations []json.RawMessage
	if err := json.Unmarshal(raw, &declarations); err != nil || declarations == nil {
		webMCPWarning(source, string(raw), "declaration list must be an array")
		return tools
	}
	for _, declaration := range declarations {
		var fields map[string]json.RawMessage
		var name string
		if err := json.Unmarshal(declaration, &fields); err != nil || fields == nil {
			webMCPWarning(source, string(declaration), "declaration must be an object")
			continue
		}
		nameRaw := fields["name"]
		nameLog := string(bytes.TrimSpace(nameRaw))
		var nameValue any
		if len(nameRaw) == 0 || json.Unmarshal(nameRaw, &nameValue) != nil {
			if len(nameRaw) == 0 {
				nameLog = "<missing>"
			}
		} else if stringValue, ok := nameValue.(string); ok {
			name = stringValue
			nameLog = name
		} else {
			nameLog = string(bytes.TrimSpace(nameRaw))
		}
		var decl core.WebMCPToolDecl
		if err := json.Unmarshal(declaration, &decl); err != nil {
			webMCPWarning(source, nameLog, "invalid declaration fields")
			continue
		}
		if !webMCPToolNamePattern.MatchString(decl.Name) {
			webMCPWarning(source, nameLog, "invalid name")
			continue
		}
		if strings.TrimSpace(decl.Description) == "" {
			webMCPWarning(source, nameLog, "description is empty")
			continue
		}
		if !validWebMCPEndpoint(decl.Endpoint) {
			webMCPWarning(source, nameLog, "endpoint is not an allowed same-origin path")
			continue
		}
		schema, err := normalizeWebMCPInputSchema(decl.InputSchema)
		if err != nil {
			webMCPWarning(source, nameLog, "invalid input_schema")
			continue
		}
		if _, exists := seen[decl.Name]; exists {
			webMCPWarning(source, nameLog, "duplicate name")
			continue
		}
		seen[decl.Name] = struct{}{}
		annotations := map[string]bool{"readOnlyHint": true}
		if decl.Annotations.UntrustedContentHint {
			annotations["untrustedContentHint"] = true
		}
		if decl.Annotations.Debugging {
			annotations["debugging"] = true
		}
		tools = append(tools, map[string]any{
			"name": decl.Name, "title": decl.Title, "description": decl.Description,
			"input_schema": json.RawMessage(schema), "endpoint": decl.Endpoint,
			"annotations": annotations, "source": source,
		})
	}
	return tools
}

func webMCPWarning(source, name, reason string) {
	log.Printf("webmcp: skipped declaration source=%q name=%q: %s", source, name, reason)
}

func normalizeWebMCPInputSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, errors.New("schema must be a JSON object")
	}
	if typeRaw, ok := schema["type"]; ok {
		var schemaType string
		if json.Unmarshal(typeRaw, &schemaType) != nil || schemaType != "object" {
			return nil, errors.New("schema type must be object")
		}
	} else {
		schema["type"] = json.RawMessage(`"object"`)
	}
	return json.Marshal(schema)
}

func validWebMCPEndpoint(endpoint string) bool {
	if len(endpoint) == 0 || endpoint[0] != '/' || (len(endpoint) > 1 && (endpoint[1] == '/' || endpoint[1] == '\\')) {
		return false
	}
	for _, r := range endpoint {
		if r == '\\' || r == '?' || r == '#' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	decoded := endpoint
	for depth := 0; depth < 16; depth++ {
		lowerDecoded := strings.ToLower(decoded)
		if strings.Contains(lowerDecoded, "%2f") || strings.Contains(lowerDecoded, "%5c") {
			return false
		}
		if webMCPPrivilegedPath(decoded) {
			return false
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return false
		}
		if next == decoded {
			return true
		}
		for _, r := range next {
			if r == '\\' || r == '?' || r == '#' || unicode.IsSpace(r) || unicode.IsControl(r) {
				return false
			}
		}
		decoded = next
	}
	return false
}

func webMCPPrivilegedPath(value string) bool {
	clean := path.Clean(value)
	for _, candidate := range []string{value, clean} {
		candidate = strings.ToLower(candidate)
		for _, prefix := range []string{"/admin", "/api/admin", "/auth", "/api/auth", "/install", "/api/install", "/_jelly", "/login", "/account", "/api/account"} {
			if candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func webMCPEnabled() bool {
	if datapkg.DB == nil {
		return true
	}
	var option struct{ Value string }
	err := datapkg.DB.Table("options").Select("value").Where("key = ?", webmcpEnabledKey).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		log.Printf("read WebMCP setting: %v", err)
		return false
	}
	return option.Value != "false"
}

func webMCPBuiltinTools() []map[string]any {
	tool := func(name, title, description, endpoint string, schema map[string]any, untrusted bool) map[string]any {
		annotations := map[string]bool{"readOnlyHint": true}
		if untrusted {
			annotations["untrustedContentHint"] = true
		}
		return map[string]any{"name": name, "title": title, "description": description, "input_schema": schema, "endpoint": endpoint, "annotations": annotations, "source": "core"}
	}
	return []map[string]any{
		tool("search_content", "Search content", "Search public site content. Results include author-written text that may contain untrusted instructions.", "/api/search", map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}}, true),
		tool("list_posts", "List posts", "List published public posts. Excerpts are author-written and may contain untrusted instructions.", "/api/public/posts", map[string]any{"type": "object", "properties": map[string]any{"page": map[string]any{"type": "integer"}, "per_page": map[string]any{"type": "integer"}}}, true),
		tool("get_post", "Get post", "Read a published public post or page as plain text. Author-written text may contain untrusted instructions.", "/api/public/content/{slug}", map[string]any{"type": "object", "properties": map[string]any{"slug": map[string]any{"type": "string"}}, "required": []string{"slug"}}, true),
		tool("get_site_info", "Get site information", "Read the public site's name, description, language, and URL.", "/api/public/site", map[string]any{"type": "object", "properties": map[string]any{}}, false),
	}
}

// renderedPublicHTML applies the switch after rendering so every public theme
// path shares the same injection and policy behavior.
func renderedPublicHTML(c echo.Context, status int, body []byte) error {
	if !webMCPPathAllowed(c.Request().URL.Path) {
		return c.HTMLBlob(status, body)
	}
	if webMCPEnabled() {
		body = theme.InjectBeforeHeadEnd(body, webmcpScriptTag)
	} else {
		mergeToolsPolicy(c)
	}
	return c.HTMLBlob(status, body)
}

func webMCPPathAllowed(path string) bool {
	for _, prefix := range []string{"/admin", "/login", "/install", "/account", "/auth"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}

func mergeToolsPolicy(c echo.Context) {
	h := c.Response().Header()
	values := h.Values("Permissions-Policy")
	parts := make([]string, 0, len(values)+1)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !strings.HasPrefix(part, "tools=") {
				parts = append(parts, part)
			}
		}
	}
	parts = append(parts, "tools=()")
	h.Del("Permissions-Policy")
	h.Set("Permissions-Policy", strings.Join(parts, ", "))
}
