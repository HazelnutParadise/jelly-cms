package server

import (
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/integration"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// machineAPIBasePath is the base path of the machine API. Ticket 24 moved it from
// /api/integration/v1 and removed the old path without an alias. Routes, the discovery
// document and the upload and maintenance allowlists all read it from here.
const machineAPIBasePath = "/api/v1"

type integrationRouteAuth string

const (
	integrationRoutePublic integrationRouteAuth = "public"
	integrationRouteKey    integrationRouteAuth = "key"
)

type integrationRoute struct {
	Method        string
	Path          string
	Permission    integration.Permission
	Summary       string
	RequiredRoles []string
	VersionGuard  bool
	Auth          integrationRouteAuth
	Handler       echo.HandlerFunc
}

func integrationRouteTable(tm *theme.Manager, rt *plugin.Runtime) []integrationRoute {
	return []integrationRoute{
		{http.MethodGet, "", "", "Discover the API", nil, false, integrationRoutePublic, integrationDiscovery(tm, rt)},
		{http.MethodGet, "/openapi.json", "", "Get the OpenAPI 3.1 document", nil, false, integrationRoutePublic, integrationOpenAPI(tm, rt)},
		{http.MethodGet, "/me", "", "Get this integration and its effective permissions", nil, false, integrationRouteKey, integrationMe},
		{http.MethodGet, "/logs", integration.LogsRead, "List this integration's request events", adminRoles, false, integrationRouteKey, listIntegrationOwnEvents},
		{http.MethodGet, "/posts", integration.PostsRead, "List posts", contentRoles, false, integrationRouteKey, integrationPostList("post")},
		{http.MethodGet, "/posts/:id", integration.PostsRead, "Get a post", contentRoles, false, integrationRouteKey, integrationTypeAndVersion("post", false, getPost)},
		{http.MethodGet, "/categories", integration.PostsRead, "List post categories", contentRoles, false, integrationRouteKey, listCategories},
		{http.MethodGet, "/tags", integration.PostsRead, "List post tags", contentRoles, false, integrationRouteKey, listTags},
		{http.MethodPost, "/posts", integration.PostsWrite, "Create a post", contentRoles, false, integrationRouteKey, integrationTypeAndVersion("post", false, createPost)},
		{http.MethodPut, "/posts/:id", integration.PostsWrite, "Update a post with base_updated_at", contentRoles, true, integrationRouteKey, integrationTypeAndVersion("post", true, updatePost)},
		{http.MethodDelete, "/posts/:id", integration.PostsWrite, "Move a post to the trash", contentRoles, false, integrationRouteKey, integrationTypeAndVersion("post", false, deletePost)},
		{http.MethodGet, "/pages", integration.PagesRead, "List pages", contentRoles, false, integrationRouteKey, integrationPostList("page")},
		{http.MethodGet, "/pages/:id", integration.PagesRead, "Get a page", contentRoles, false, integrationRouteKey, integrationTypeAndVersion("page", false, getPost)},
		{http.MethodPost, "/pages", integration.PagesWrite, "Create a page", editorRoles, false, integrationRouteKey, integrationTypeAndVersion("page", false, createPost)},
		{http.MethodPut, "/pages/:id", integration.PagesWrite, "Update a page with base_updated_at", editorRoles, true, integrationRouteKey, integrationTypeAndVersion("page", true, updatePost)},
		{http.MethodDelete, "/pages/:id", integration.PagesWrite, "Move a page to the trash", editorRoles, false, integrationRouteKey, integrationTypeAndVersion("page", false, deletePost)},
		{http.MethodGet, "/media", integration.MediaRead, "List legacy media", contentRoles, false, integrationRouteKey, listLegacyMedia},
		{http.MethodPost, "/media", integration.MediaWrite, "Upload legacy media", contentRoles, false, integrationRouteKey, handleLegacyMediaUpload},
		{http.MethodPut, "/media/:filename", integration.MediaWrite, "Replace legacy media", contentRoles, false, integrationRouteKey, handleLegacyMediaReplace},
		{http.MethodDelete, "/media/:filename", integration.MediaWrite, "Delete legacy media", contentRoles, false, integrationRouteKey, handleLegacyMediaDelete},
		{http.MethodPost, "/media/rename", integration.MediaWrite, "Rename legacy media", contentRoles, false, integrationRouteKey, renameLegacyMedia},
		{http.MethodGet, "/media/files", integration.MediaRead, "List media files", contentRoles, false, integrationRouteKey, handleMediaFileList},
		{http.MethodPost, "/media/files", integration.MediaWrite, "Upload a media file", contentRoles, false, integrationRouteKey, handleMediaFileUpload},
		{http.MethodPatch, "/media/files/:id", integration.MediaWrite, "Update media file metadata", contentRoles, false, integrationRouteKey, handleMediaFileUpdate},
		{http.MethodDelete, "/media/files/:id", integration.MediaWrite, "Move a media file to trash", contentRoles, false, integrationRouteKey, handleMediaFileTrash},
		{http.MethodPost, "/media/files/:id/replace", integration.MediaWrite, "Replace a media file", contentRoles, false, integrationRouteKey, handleMediaFileReplace},
		{http.MethodGet, "/products", integration.ProductsRead, "List products", adminRoles, false, integrationRouteKey, adminProductsList},
		{http.MethodGet, "/products/:id", integration.ProductsRead, "Get a product", adminRoles, false, integrationRouteKey, adminProductsGet},
		{http.MethodPost, "/products", integration.ProductsWrite, "Create a product", adminRoles, false, integrationRouteKey, adminProductsCreate},
		{http.MethodPut, "/products/:id", integration.ProductsWrite, "Update a product with base_updated_at", adminRoles, true, integrationRouteKey, integrationTypeAndVersion("", true, adminProductsUpdate)},
		{http.MethodDelete, "/products/:id", integration.ProductsWrite, "Move a product to the trash", adminRoles, false, integrationRouteKey, adminProductsDelete},
		{http.MethodGet, "/orders", integration.OrdersRead, "List orders", adminRoles, false, integrationRouteKey, adminOrdersList},
		{http.MethodGet, "/orders/:id", integration.OrdersRead, "Get an order", adminRoles, false, integrationRouteKey, adminOrderGet},
		{http.MethodGet, "/themes", integration.ThemesRead, "List themes", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return listThemes(c, tm) }},
		{http.MethodGet, "/themes/:name/config", integration.ThemesRead, "Get theme configuration", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return getThemeConfig(c, tm) }},
		{http.MethodGet, "/themes/:name/settings", integration.ThemesRead, "Get theme settings", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return getThemeSettings(c, tm) }},
		{http.MethodPost, "/themes/activate", integration.ThemesWrite, "Activate a theme", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return activateTheme(c, tm) }},
		{http.MethodPost, "/themes/:name/settings", integration.ThemesWrite, "Save theme settings", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return saveThemeSettings(c, tm) }},
		{http.MethodGet, "/plugins", integration.PluginsRead, "List plugins", adminRoles, false, integrationRouteKey, listPlugins},
		{http.MethodPost, "/plugins/:id/reload", integration.PluginsWrite, "Reload a plugin", adminRoles, false, integrationRouteKey, func(c echo.Context) error { return reloadPlugin(c, rt) }},
		{http.MethodGet, "/settings", integration.SettingsRead, "Read masked system settings", adminRoles, false, integrationRouteKey, handleSettingsList},
		{http.MethodPost, "/settings", integration.SettingsWrite, "Write system settings", adminRoles, false, integrationRouteKey, handleSettingsWrite},
		{http.MethodPut, "/settings", integration.SettingsWrite, "Write system settings", adminRoles, false, integrationRouteKey, handleSettingsWrite},
	}
}

func integrationPostList(kind string) echo.HandlerFunc {
	return func(c echo.Context) error { return integrationTypeAndVersion(kind, false, listPosts)(c) }
}

func integrationRoutePathMatches(pattern, path string) bool {
	a := strings.Split(strings.Trim(pattern, "/"), "/")
	b := strings.Split(strings.Trim(path, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i, p := range a {
		if strings.HasPrefix(p, ":") {
			continue
		}
		if p != b[i] {
			return false
		}
	}
	return true
}
func integrationAdminPageGate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u, _ := auth.GetUserFromContext(c)
		if u == nil || u.Role != "admin" {
			return echo.NewHTTPError(http.StatusForbidden, "Forbidden")
		}
		return next(c)
	}
}

func integrationRoutePermissionContext(permission integration.Permission) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if permission != "" {
				c.Set("integration.permission", string(permission))
			}
			return next(c)
		}
	}
}

func integrationFallbackPermissionContext(tm *theme.Manager, rt *plugin.Runtime) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := strings.TrimPrefix(c.Request().URL.Path, machineAPIBasePath)
			for _, route := range integrationRouteTable(tm, rt) {
				if route.Permission != "" && integrationRoutePathMatches(route.Path, path) {
					c.Set("integration.permission", string(route.Permission))
					break
				}
			}
			return next(c)
		}
	}
}

func registerIntegrationRoutes(e *echo.Echo, tm *theme.Manager, rt *plugin.Runtime, renderAdmin func(c echo.Context, file, active string) error) {
	manage := e.Group("/api/admin/agents", auth.RequireRole(adminRoles...))
	manage.GET("", listIntegrations)
	manage.GET("/events", listIntegrationAdminEvents)
	manage.GET("/:id", getIntegration)
	interactive := auth.RequireInteractive()
	manage.POST("", createIntegration, interactive)
	manage.PATCH("/:id", patchIntegration, interactive)
	manage.DELETE("/:id", deleteIntegration, interactive)
	manage.POST("/:id/rotate", rotateIntegrationKey, interactive)
	manage.POST("/:id/keys/:key_id/revoke", revokeIntegrationKey, interactive)
	manage.POST("/:id/disable", disableIntegration, interactive)
	manage.POST("/:id/enable", enableIntegration, interactive)
	api := e.Group(machineAPIBasePath)
	for _, r := range integrationRouteTable(tm, rt) {
		h := r.Handler
		middlewares := []echo.MiddlewareFunc{integrationErrorEnvelope, integrationRoutePermissionContext(r.Permission)}
		if r.Auth == integrationRouteKey {
			middlewares = append(middlewares, integrationKeyMiddleware, integrationRequestAudit, withIntegrationKeyTouch)
		} else {
			middlewares = append(middlewares, integrationRequestAudit)
		}
		if r.Permission != "" {
			middlewares = append(middlewares, integrationPermissionMiddleware(r))
		}
		if (r.Path == "/media" && r.Method == http.MethodPost) || (r.Path == "/media/:filename" && r.Method == http.MethodPut) || (r.Path == "/media/files" && r.Method == http.MethodPost) || (r.Path == "/media/files/:id/replace" && r.Method == http.MethodPost) {
			middlewares = append(middlewares, middleware.BodyLimit(uploadBodyLimit))
		} else if r.Auth == integrationRouteKey {
			middlewares = append(middlewares, middleware.BodyLimit(globalBodyLimit))
		}
		if r.Path == "/settings" && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			middlewares = append(middlewares, integrationSettingsURLGuard)
		}
		api.Add(r.Method, r.Path, h, middlewares...)
	}
	api.Any("/*", func(c echo.Context) error {
		for _, r := range integrationRouteTable(tm, rt) {
			if integrationRoutePathMatches(r.Path, c.Request().URL.Path[len(machineAPIBasePath):]) {
				return writeAPIError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
			}
		}
		return writeAPIError(c, http.StatusNotFound, "not_found", "API endpoint not found", nil)
	}, integrationErrorEnvelope, integrationFallbackPermissionContext(tm, rt), integrationKeyMiddleware, integrationRequestAudit, middleware.BodyLimit(globalBodyLimit))
	api.Match([]string{http.MethodHead, http.MethodConnect, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace}, "", func(c echo.Context) error {
		return writeAPIError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}, integrationErrorEnvelope, integrationFallbackPermissionContext(tm, rt), integrationKeyMiddleware, integrationRequestAudit, middleware.BodyLimit(globalBodyLimit))
	pages := e.Group("/admin", auth.RequireAuth, integrationAdminPageGate)
	pages.GET("/integrations", func(c echo.Context) error { return renderAdmin(c, "agents.html", "agents") })
	pages.GET("/integrations/:id", func(c echo.Context) error { return renderAdmin(c, "agent_detail.html", "agents") })
	// Ticket 24 moved the pages to /admin/integrations. The old paths redirect and keep the id and query string.
	pages.GET("/agents", redirectToIntegrations)
	pages.GET("/agents/:id", redirectToIntegrations)
}

func redirectToIntegrations(c echo.Context) error {
	target := "/admin/integrations" + strings.TrimPrefix(c.Request().URL.Path, "/admin/agents")
	if q := c.Request().URL.RawQuery; q != "" {
		target += "?" + q
	}
	return c.Redirect(http.StatusFound, target)
}

func init() {
	RegisterNav(NavItem{Key: "agents", Section: "system", Label: "agents", Icon: "i-key", Href: "/admin/integrations", Order: 85, Roles: []string{"admin"}})
}
