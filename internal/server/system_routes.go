package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/labstack/echo/v4"
)

// System routes (docs/tickets/08-system.md). Handlers for these endpoints are
// filled in by tickets 08a-08d. Until then every endpoint below answers 501
// Not Implemented, except /healthz, which is live.
//
// Public (no login):
//
//	GET  /robots.txt                       seo.Robots
//	GET  /sitemap.xml                      seo.SitemapIndex
//	GET  /sitemap-{posts|pages|categories|products}-{n}.xml   seo.SitemapPart
//	GET  /llms.txt                         seo.LLMSTxt
//	GET  /.well-known/tdmrep.json          seo.TDMRep
//	GET  /feed.xml                         seo.Feed (live, feed_routes.go)
//	GET  /c/:slug/feed.xml                 seo.Feed (category)
//	GET  /author/:u/feed.xml               seo.Feed (author)
//	GET  /<indexnow_key>.txt               registered by 08b from seo_indexnow_key
//	GET  /healthz                          200 {"status":"ok"}
//
// Admin pages (admin role; sit beside the existing pages in routes.go):
//
//	/admin/settings, /admin/settings/:tab  settings_tabs.go
//	/admin/redirects                       redirects.html (placeholder)
//	/admin/logs                            logs.html (placeholder)
//	/admin/tools/backup                    tools_backup.html (placeholder)
//	/admin/tools/import                    tools_import.html (placeholder)
//	/admin/store                           store.html (placeholder)
//	/admin/plugins/:id                     plugin_detail.html (placeholder)
//	/admin/media/policies                  media_policies.html (placeholder)
//
// Admin API (/api/admin/*, admin role). Rows marked [I] are interactive-only
// (see ticket 08 「互動限定」). Endpoints already implemented elsewhere
// (GET|POST /settings, POST /plugins/upload, POST /plugins/:id/reload and the
// legacy /media routes) are not repeated here.
//
//	Settings
//	  PUT    /settings                         POST /settings is the live write path
//	  GET    /settings/injection               PUT[I] /settings/injection
//	  GET    /maintenance                      PUT    /maintenance
//	SEO
//	  GET    /seo/robots-preview   GET /seo/crawler-stats   GET /seo/overrides
//	Redirects and 404s (live, redirects_api.go)
//	  GET    /redirects                        POST   /redirects
//	  PUT    /redirects/:id                    DELETE /redirects/:id
//	  POST   /redirects/test                   POST[I] /redirects/import
//	  GET    /redirects/export.csv
//	  GET    /not-found                        POST   /not-found/:id/ignore
//	  DELETE /not-found
//	Logs
//	  GET    /logs                             GET    /logs/stream (SSE)
//	  GET[I] /logs/export                      GET    /logs/summary
//	Update
//	  GET    /update                           POST   /update/check
//	  POST[I] /update/apply                    POST   /update/cancel
//	  POST[I] /update/rollback                 PUT    /update/settings
//	Infrastructure, queue and mail
//	  GET    /infra                            PUT    /infra/cache
//	  POST   /infra/cache/test                 POST   /infra/cache/flush
//	  PUT    /infra/queue                      POST   /infra/queue/test
//	  POST   /infra/queue/switch               POST   /infra/queue/pause
//	  POST   /infra/queue/resume
//	  GET    /jobs                             POST   /jobs/:id/retry
//	  POST   /jobs/retry-failed                DELETE /jobs/:id
//	  GET|PUT /mail/settings                   POST   /mail/test
//	Backups and import
//	  GET    /backups                          POST[I] /backups
//	  POST   /backups/:id/retry                GET[I] /backups/:id/download
//	  POST[I] /backups/:id/restore             DELETE /backups/:id
//	  PUT    /backups/schedule                 POST[I] /export
//	  GET[I] /exports/:id/download
//	  POST   /import/wxr                       GET    /import
//	  GET    /import/:id                       PUT    /import/:id/mapping
//	  POST   /import/:id/dry-run               POST[I] /import/:id/run
//	  POST   /import/:id/cancel
//	Store and plugins
//	  GET    /store                            POST   /store/refresh
//	  POST[I] /store/install                   POST[I] /store/update
//	  POST[I] /plugins/install-url             GET    /plugins/:id
//	  POST   /plugins/:id/enable               POST   /plugins/:id/disable
//	  DELETE[I] /plugins/:id                   GET|PUT /plugins/:id/settings
//	  POST   /plugins/:id/settings/reset       POST   /plugins/:id/test
//	  POST   /plugins/:id/actions/:action      GET    /plugins/:id/logs
//	  GET    /extensions                       PUT    /extensions/:point
//	Media policies, groups and files: media_api.go (live)
//
// Live in this branch: /healthz, the settings tabs, uploads, the WXR import API (import_api.go), the update API (update_api.go), the plugin detail API (plugin_detail_api.go), the store and install-from-URL API (store_api.go), the attachment API (media_api.go) and the admin pages above.
type systemEndpoint struct {
	Method string
	Path   string
}

// systemAdminEndpoints lists the admin API endpoints that are not implemented
// yet. Each one answers 501 for admins and 401/403 for everyone else.
var systemAdminEndpoints = []systemEndpoint{
	{http.MethodGet, "/api/admin/mail/settings"},
	{http.MethodPut, "/api/admin/mail/settings"},
	{http.MethodPost, "/api/admin/mail/test"},
}

// systemPublicEndpoints are served without login. They are registered before
// the /:slug catch-all, which is registered last in RegisterRoutes.
var systemPublicEndpoints = []systemEndpoint{
	{http.MethodGet, "/c/:slug/feed.xml"},
	{http.MethodGet, "/author/:u/feed.xml"},
}

// registerSystemRoutes mounts the system endpoints and pages. It must run
// before the /:slug catch-all is registered.
func registerSystemRoutes(e *echo.Echo, pluginRuntime *plugin.Runtime) {
	registerLogRoutes(e)
	registerInfraRoutes(e)
	registerRedirectRoutes(e)
	registerFeedRoutes(e)
	registerSEORoutes(e)
	registerInjectionRoutes(e)
	registerMaintenanceRoutes(e)
	registerBackupRoutes(e)
	registerImportRoutes(e)
	registerUpdateRoutes(e)
	registerPluginDetailRoutes(e, pluginRuntime)
	registerStoreRoutes(e, pluginRuntime)
	registerMediaRoutes(e)
	for _, ep := range systemAdminEndpoints {
		e.Add(ep.Method, ep.Path, notImplemented, auth.RequireRole("admin"))
	}
	for _, ep := range systemPublicEndpoints {
		e.Add(ep.Method, ep.Path, notImplemented)
	}
	e.GET("/healthz", handleHealthz)

	pages := e.Group("/admin", auth.RequireRole("admin"))
	pages.GET("/redirects", renderPlaceholder("redirects", "redirects"))
	pages.GET("/logs", renderPlaceholder("logs", "logs"))
	pages.GET("/tools/backup", renderPlaceholder("tools", "tools"))
	pages.GET("/tools/import", renderPlaceholder("tools", "importWordPress"))
	pages.GET("/store", renderPlaceholder("store", "store"))
	pages.GET("/plugins/:id", func(c echo.Context) error {
		return renderAdminPage(c, AdminPage{
			File:   "placeholder.html",
			Active: "plugins",
			Data:   map[string]interface{}{"PageTitle": "plugins", "PluginID": c.Param("id")},
		})
	})
	pages.GET("/media/policies", renderPlaceholder("media", "media"))
}

// renderPlaceholder renders the placeholder page for an admin screen. The
// screen's real content arrives with its ticket.
func renderPlaceholder(active, titleKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return renderAdminPage(c, AdminPage{
			File:   "placeholder.html",
			Active: active,
			Data:   map[string]interface{}{"PageTitle": titleKey},
		})
	}
}

// handleHealthz reports that the process is serving requests. It does not
// touch the database, so it answers before installation finishes.
func handleHealthz(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func init() {
	adminOnly := []string{"admin"}
	RegisterNav(
		NavItem{Key: "store", Section: "appearance", Label: "store", Icon: "i-download", Href: "/admin/store", Order: 30, Roles: adminOnly},
		NavItem{Key: "redirects", Section: "system", Label: "redirects", Icon: "i-redirect", Href: "/admin/redirects", Order: 100, Roles: adminOnly},
		NavItem{Key: "tools", Section: "system", Label: "tools", Icon: "i-tools", Href: "/admin/tools/backup", Order: 110, Roles: adminOnly},
		NavItem{Key: "logs", Section: "system", Label: "logs", Icon: "i-terminal", Href: "/admin/logs", Order: 120, Roles: adminOnly},
	)
	RegisterPaletteCommands(
		PaletteCommand{Key: "backup-now", Label: "backupNow", Keywords: []string{"backup", "備份"}, Icon: "i-download", Href: "/admin/tools/backup", Roles: adminOnly},
		PaletteCommand{Key: "clear-cache", Label: "clearCache", Keywords: []string{"cache", "快取"}, Icon: "i-refresh", Href: "/admin/settings/infra", Roles: adminOnly},
		PaletteCommand{Key: "view-logs", Label: "viewLogs", Keywords: []string{"logs", "log", "日誌"}, Icon: "i-terminal", Href: "/admin/logs", Roles: adminOnly},
		PaletteCommand{Key: "import-wordpress", Label: "importWordPress", Keywords: []string{"wordpress", "wxr", "import", "匯入"}, Icon: "i-wp", Href: "/admin/tools/import", Roles: adminOnly},
	)
}
