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
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

const (
	// webmcpEnabledKey is the option row that switches WebMCP; a missing row means "true".
	webmcpEnabledKey = "webmcp_enabled"
	// webmcpScriptTag is inserted once into every public theme page while WebMCP is on.
	webmcpScriptTag = `<script type="module" src="/_jelly/webmcp.js"></script>`
	// webmcpScriptFile is the vendored script served at /_jelly/webmcp.js.
	webmcpScriptFile = "web/jelly/webmcp.js"
)

func registerWebMCPRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	e.GET("/_jelly/webmcp.js", func(c echo.Context) error {
		// TODO(ticket 13): serve webmcpScriptFile as text/javascript; 404 while disabled.
		return echo.NewHTTPError(http.StatusNotImplemented)
	})
	e.GET("/_jelly/webmcp-tools.json", func(c echo.Context) error {
		// TODO(ticket 13): merge core, plugin and theme tools; 404 while disabled.
		return echo.NewHTTPError(http.StatusNotImplemented)
	})
}
