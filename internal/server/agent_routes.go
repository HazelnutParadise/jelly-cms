package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// AI agent API (docs/tickets/14-agent-api.md).
//
//	/api/admin/agents/*   management, signed-in admins only (M1–M9)
//	/api/agent/v1/*       the agent-facing API, agent keys only (A1–A4 and the
//	                      permission table, which reuses the /api/admin handlers)
//	/admin/agents[/:id]   admin screens (Agents and AgentDetail boards)
//
// This file is the ticket 14 skeleton: every API route answers 501 and the
// pages render empty templates. agentRouteTable is the single source the
// implementation registers routes, the discovery index and the OpenAPI
// document from.

// agentRouteAuth says whether an agent route needs a key.
type agentRouteAuth string

const (
	agentRoutePublic agentRouteAuth = "public"
	agentRouteKey    agentRouteAuth = "key"
)

// agentRoute is one row of the agent route table.
type agentRoute struct {
	// Method is the HTTP method, e.g. http.MethodGet.
	Method string
	// Path is relative to /api/agent/v1 in Echo form, e.g. "/posts/:id"; ""
	// is the discovery index itself.
	Path string
	// Permission is the one permission the route needs; empty for /me and the
	// public documentation routes.
	Permission agent.Permission
	// Summary is the one-line description used by the index and OpenAPI.
	Summary string
	// RequiredRoles is the role set the reused handler has on /api/admin. It
	// must be the same variable the /api/admin group uses, never a copy.
	RequiredRoles []string
	// VersionGuard marks updates that require base_updated_at.
	VersionGuard bool
	// Auth is agentRoutePublic or agentRouteKey.
	Auth agentRouteAuth
	// Handler is the reused /api/admin handler or an agent-only handler.
	Handler echo.HandlerFunc
}

// agentRouteTable returns every /api/agent/v1 route.
func agentRouteTable(tm *theme.Manager, pluginRuntime *plugin.Runtime) []agentRoute {
	// TODO(ticket 14): fill in from the permission table in the ticket.
	return nil
}

// agentNotImplemented answers every skeleton route.
func agentNotImplemented(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]any{
		"code":    "not_implemented",
		"message": "TODO(ticket 14)",
		"details": map[string]any{},
	})
}

func registerAgentRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime, renderAdmin func(c echo.Context, file, active string) error) {
	// Management API (M1–M9): signed-in admins only. TODO(ticket 14): real
	// handlers; write endpoints get ticket 06's RequireInteractive once it lands.
	manage := e.Group("/api/admin/agents", auth.RequireRole("admin"))
	manage.GET("", agentNotImplemented)
	manage.POST("", agentNotImplemented)
	manage.GET("/events", agentNotImplemented)
	manage.GET("/:id", agentNotImplemented)
	manage.PATCH("/:id", agentNotImplemented)
	manage.DELETE("/:id", agentNotImplemented)
	manage.POST("/:id/rotate", agentNotImplemented)
	manage.POST("/:id/keys/:key_id/revoke", agentNotImplemented)
	manage.POST("/:id/disable", agentNotImplemented)
	manage.POST("/:id/enable", agentNotImplemented)

	// Agent API (A1–A4 and the permission table). TODO(ticket 14): register
	// agentRouteTable with the key, permission, version-guard and audit
	// middleware; unmatched paths answer the not_found envelope.
	api := e.Group("/api/agent/v1")
	api.GET("", agentNotImplemented)
	api.GET("/openapi.json", agentNotImplemented)
	api.GET("/me", agentNotImplemented)
	api.GET("/logs", agentNotImplemented)
	api.Any("/*", agentNotImplemented)

	// Admin screens. TODO(ticket 14): admin only (other roles 403).
	pages := e.Group("/admin", auth.RequireAuth)
	pages.GET("/agents", func(c echo.Context) error { return renderAdmin(c, "agents.html", "agents") })
	pages.GET("/agents/:id", func(c echo.Context) error { return renderAdmin(c, "agent_detail.html", "agents") })
}

func init() {
	RegisterNav(NavItem{Key: "agents", Section: "system", Label: "agents", Icon: "i-key", Href: "/admin/agents", Order: 85, Roles: []string{"admin"}})
}
