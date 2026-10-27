package server

import (
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type agentRouteAuth string

const (
	agentRoutePublic agentRouteAuth = "public"
	agentRouteKey    agentRouteAuth = "key"
)

type agentRoute struct {
	Method        string
	Path          string
	Permission    agent.Permission
	Summary       string
	RequiredRoles []string
	VersionGuard  bool
	Auth          agentRouteAuth
	Handler       echo.HandlerFunc
}

func agentRouteTable(tm *theme.Manager, rt *plugin.Runtime) []agentRoute {
	return []agentRoute{
		{http.MethodGet, "", "", "Discover the agent API", nil, false, agentRoutePublic, agentDiscovery(tm, rt)},
		{http.MethodGet, "/openapi.json", "", "Get the OpenAPI 3.1 document", nil, false, agentRoutePublic, agentOpenAPI(tm, rt)},
		{http.MethodGet, "/me", "", "Get this agent and its effective permissions", nil, false, agentRouteKey, agentMe},
		{http.MethodGet, "/logs", agent.LogsRead, "List this agent's request events", adminRoles, false, agentRouteKey, listAgentOwnEvents},
		{http.MethodGet, "/posts", agent.PostsRead, "List posts", contentRoles, false, agentRouteKey, agentPostList("post")},
		{http.MethodGet, "/posts/:id", agent.PostsRead, "Get a post", contentRoles, false, agentRouteKey, agentTypeAndVersion("post", false, getPost)},
		{http.MethodGet, "/categories", agent.PostsRead, "List post categories", contentRoles, false, agentRouteKey, listCategories},
		{http.MethodGet, "/tags", agent.PostsRead, "List post tags", contentRoles, false, agentRouteKey, listTags},
		{http.MethodPost, "/posts", agent.PostsWrite, "Create a post", contentRoles, false, agentRouteKey, agentTypeAndVersion("post", false, createPost)},
		{http.MethodPut, "/posts/:id", agent.PostsWrite, "Update a post with base_updated_at", contentRoles, true, agentRouteKey, agentTypeAndVersion("post", true, updatePost)},
		{http.MethodDelete, "/posts/:id", agent.PostsWrite, "Move a post to the trash", contentRoles, false, agentRouteKey, agentTypeAndVersion("post", false, deletePost)},
		{http.MethodGet, "/pages", agent.PagesRead, "List pages", contentRoles, false, agentRouteKey, agentPostList("page")},
		{http.MethodGet, "/pages/:id", agent.PagesRead, "Get a page", contentRoles, false, agentRouteKey, agentTypeAndVersion("page", false, getPost)},
		{http.MethodPost, "/pages", agent.PagesWrite, "Create a page", editorRoles, false, agentRouteKey, agentTypeAndVersion("page", false, createPost)},
		{http.MethodPut, "/pages/:id", agent.PagesWrite, "Update a page with base_updated_at", editorRoles, true, agentRouteKey, agentTypeAndVersion("page", true, updatePost)},
		{http.MethodDelete, "/pages/:id", agent.PagesWrite, "Move a page to the trash", editorRoles, false, agentRouteKey, agentTypeAndVersion("page", false, deletePost)},
		{http.MethodGet, "/media", agent.MediaRead, "List legacy media", contentRoles, false, agentRouteKey, listLegacyMedia},
		{http.MethodPost, "/media", agent.MediaWrite, "Upload legacy media", contentRoles, false, agentRouteKey, handleLegacyMediaUpload},
		{http.MethodPut, "/media/:filename", agent.MediaWrite, "Replace legacy media", contentRoles, false, agentRouteKey, handleLegacyMediaReplace},
		{http.MethodDelete, "/media/:filename", agent.MediaWrite, "Delete legacy media", contentRoles, false, agentRouteKey, handleLegacyMediaDelete},
		{http.MethodPost, "/media/rename", agent.MediaWrite, "Rename legacy media", contentRoles, false, agentRouteKey, renameLegacyMedia},
		{http.MethodGet, "/media/files", agent.MediaRead, "List media files", contentRoles, false, agentRouteKey, handleMediaFileList},
		{http.MethodPost, "/media/files", agent.MediaWrite, "Upload a media file", contentRoles, false, agentRouteKey, handleMediaFileUpload},
		{http.MethodPatch, "/media/files/:id", agent.MediaWrite, "Update media file metadata", contentRoles, false, agentRouteKey, handleMediaFileUpdate},
		{http.MethodDelete, "/media/files/:id", agent.MediaWrite, "Move a media file to trash", contentRoles, false, agentRouteKey, handleMediaFileTrash},
		{http.MethodPost, "/media/files/:id/replace", agent.MediaWrite, "Replace a media file", contentRoles, false, agentRouteKey, handleMediaFileReplace},
		{http.MethodGet, "/products", agent.ProductsRead, "List products", adminRoles, false, agentRouteKey, adminProductsList},
		{http.MethodGet, "/products/:id", agent.ProductsRead, "Get a product", adminRoles, false, agentRouteKey, adminProductsGet},
		{http.MethodPost, "/products", agent.ProductsWrite, "Create a product", adminRoles, false, agentRouteKey, adminProductsCreate},
		{http.MethodPut, "/products/:id", agent.ProductsWrite, "Update a product with base_updated_at", adminRoles, true, agentRouteKey, agentTypeAndVersion("", true, adminProductsUpdate)},
		{http.MethodDelete, "/products/:id", agent.ProductsWrite, "Move a product to the trash", adminRoles, false, agentRouteKey, adminProductsDelete},
		{http.MethodGet, "/orders", agent.OrdersRead, "List orders", adminRoles, false, agentRouteKey, adminOrdersList},
		{http.MethodGet, "/orders/:id", agent.OrdersRead, "Get an order", adminRoles, false, agentRouteKey, adminOrderGet},
		{http.MethodGet, "/themes", agent.ThemesRead, "List themes", adminRoles, false, agentRouteKey, func(c echo.Context) error { return listThemes(c, tm) }},
		{http.MethodGet, "/themes/:name/config", agent.ThemesRead, "Get theme configuration", adminRoles, false, agentRouteKey, func(c echo.Context) error { return getThemeConfig(c, tm) }},
		{http.MethodGet, "/themes/:name/settings", agent.ThemesRead, "Get theme settings", adminRoles, false, agentRouteKey, func(c echo.Context) error { return getThemeSettings(c, tm) }},
		{http.MethodPost, "/themes/activate", agent.ThemesWrite, "Activate a theme", adminRoles, false, agentRouteKey, func(c echo.Context) error { return activateTheme(c, tm) }},
		{http.MethodPost, "/themes/:name/settings", agent.ThemesWrite, "Save theme settings", adminRoles, false, agentRouteKey, func(c echo.Context) error { return saveThemeSettings(c, tm) }},
		{http.MethodGet, "/plugins", agent.PluginsRead, "List plugins", adminRoles, false, agentRouteKey, listPlugins},
		{http.MethodPost, "/plugins/:id/reload", agent.PluginsWrite, "Reload a plugin", adminRoles, false, agentRouteKey, func(c echo.Context) error { return reloadPlugin(c, rt) }},
		{http.MethodGet, "/settings", agent.SettingsRead, "Read masked system settings", adminRoles, false, agentRouteKey, handleSettingsList},
		{http.MethodPost, "/settings", agent.SettingsWrite, "Write system settings", adminRoles, false, agentRouteKey, handleSettingsWrite},
		{http.MethodPut, "/settings", agent.SettingsWrite, "Write system settings", adminRoles, false, agentRouteKey, handleSettingsWrite},
	}
}

func agentPostList(kind string) echo.HandlerFunc {
	return func(c echo.Context) error { return agentTypeAndVersion(kind, false, listPosts)(c) }
}

func agentRoutePathMatches(pattern, path string) bool {
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
func agentAdminPageGate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u, _ := auth.GetUserFromContext(c)
		if u == nil || u.Role != "admin" {
			return echo.NewHTTPError(http.StatusForbidden, "Forbidden")
		}
		return next(c)
	}
}

func agentRoutePermissionContext(permission agent.Permission) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if permission != "" {
				c.Set("agent.permission", string(permission))
			}
			return next(c)
		}
	}
}

func agentFallbackPermissionContext(tm *theme.Manager, rt *plugin.Runtime) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := strings.TrimPrefix(c.Request().URL.Path, "/api/agent/v1")
			for _, route := range agentRouteTable(tm, rt) {
				if route.Permission != "" && agentRoutePathMatches(route.Path, path) {
					c.Set("agent.permission", string(route.Permission))
					break
				}
			}
			return next(c)
		}
	}
}

func registerAgentRoutes(e *echo.Echo, tm *theme.Manager, rt *plugin.Runtime, renderAdmin func(c echo.Context, file, active string) error) {
	manage := e.Group("/api/admin/agents", auth.RequireRole(adminRoles...))
	manage.GET("", listAgents)
	manage.GET("/events", listAgentAdminEvents)
	manage.GET("/:id", getAgent)
	interactive := auth.RequireInteractive()
	manage.POST("", createAgent, interactive)
	manage.PATCH("/:id", patchAgent, interactive)
	manage.DELETE("/:id", deleteAgent, interactive)
	manage.POST("/:id/rotate", rotateAgentKey, interactive)
	manage.POST("/:id/keys/:key_id/revoke", revokeAgentKey, interactive)
	manage.POST("/:id/disable", disableAgent, interactive)
	manage.POST("/:id/enable", enableAgent, interactive)
	api := e.Group("/api/agent/v1")
	for _, r := range agentRouteTable(tm, rt) {
		h := r.Handler
		middlewares := []echo.MiddlewareFunc{agentErrorEnvelope, agentRoutePermissionContext(r.Permission)}
		if r.Auth == agentRouteKey {
			middlewares = append(middlewares, agentKeyMiddleware, agentRequestAudit, withAgentKeyTouch)
		} else {
			middlewares = append(middlewares, agentRequestAudit)
		}
		if r.Permission != "" {
			middlewares = append(middlewares, agentPermissionMiddleware(r))
		}
		if (r.Path == "/media" && r.Method == http.MethodPost) || (r.Path == "/media/:filename" && r.Method == http.MethodPut) || (r.Path == "/media/files" && r.Method == http.MethodPost) || (r.Path == "/media/files/:id/replace" && r.Method == http.MethodPost) {
			middlewares = append(middlewares, middleware.BodyLimit(uploadBodyLimit))
		} else if r.Auth == agentRouteKey {
			middlewares = append(middlewares, middleware.BodyLimit(globalBodyLimit))
		}
		if r.Path == "/settings" && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			middlewares = append(middlewares, agentSettingsURLGuard)
		}
		api.Add(r.Method, r.Path, h, middlewares...)
	}
	api.Any("/*", func(c echo.Context) error {
		for _, r := range agentRouteTable(tm, rt) {
			if agentRoutePathMatches(r.Path, c.Request().URL.Path[len("/api/agent/v1"):]) {
				return writeAPIError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
			}
		}
		return writeAPIError(c, http.StatusNotFound, "not_found", "Agent endpoint not found", nil)
	}, agentErrorEnvelope, agentFallbackPermissionContext(tm, rt), agentKeyMiddleware, agentRequestAudit, middleware.BodyLimit(globalBodyLimit))
	api.Match([]string{http.MethodHead, http.MethodConnect, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace}, "", func(c echo.Context) error {
		return writeAPIError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
	}, agentErrorEnvelope, agentFallbackPermissionContext(tm, rt), agentKeyMiddleware, agentRequestAudit, middleware.BodyLimit(globalBodyLimit))
	pages := e.Group("/admin", auth.RequireAuth, agentAdminPageGate)
	pages.GET("/agents", func(c echo.Context) error { return renderAdmin(c, "agents.html", "agents") })
	pages.GET("/agents/:id", func(c echo.Context) error { return renderAdmin(c, "agent_detail.html", "agents") })
}

func init() {
	RegisterNav(NavItem{Key: "agents", Section: "system", Label: "agents", Icon: "i-key", Href: "/admin/agents", Order: 85, Roles: []string{"admin"}})
}
