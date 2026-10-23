package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

// User administration and invitations (docs/tickets/06-accounts.md). Skeleton:
// every route answers 501 through notImplemented until its ticket slice is
// implemented.
//
// Admin API, auth.RequireRole("admin"). "I" marks interactive-only endpoints.
//
//	A1  GET    /api/admin/users          q, role, status, page, per_page <= 100 -> {items, total, counts}
//	A2  POST   /api/admin/users/invite I {email, role, note} -> 201 {id, invite_url, expires_at, emailed}; Email taken 409
//	A3  POST   /api/admin/users/:id/invite/resend I   new token; /invite/revoke removes a pending invite
//	A4  PATCH  /api/admin/users/:id  I   {role, status active|disabled, display_name}; last active admin 409; self 409
//	A5  POST   /api/admin/users/:id/reset-password I  1-hour link -> {reset_url, emailed}; use revokes every login
//	A6  POST   /api/admin/users/:id/revoke-sessions I revoke all logins
//	A7  DELETE /api/admin/users/:id?reassign_to=  I   content without a target 409 {posts, pages, comments}; self and last admin 409
//	A8  POST   /api/admin/users/bulk          I   {ids <= 100, action disable|enable|set_role|delete} -> {updated, skipped}
//	A9  GET    /api/admin/roles                     permission matrix, read only
//	A10 GET    /invite/:token                       public; POST /api/invites/accept {token, username?, password, display_name?}
//	    GET    /password-reset/:token               public; POST /api/password-reset {token, password}
//	    Tokens are single-use; expired or used links answer 410.
//
// Pages (renderAdmin): /admin/users, with the invite panel reached through #invite.
func registerUserRoutes(e *echo.Echo, renderAdmin func(c echo.Context, file, active string) error) {
	admins := e.Group("/api/admin/users", auth.RequireRole("admin"))
	interactive := auth.RequireInteractive()

	admins.GET("", notImplemented)
	admins.POST("/invite", notImplemented, interactive)
	admins.POST("/:id/invite/resend", notImplemented, interactive)
	admins.POST("/:id/invite/revoke", notImplemented, interactive)
	admins.PATCH("/:id", notImplemented, interactive)
	admins.POST("/:id/reset-password", notImplemented, interactive)
	admins.POST("/:id/revoke-sessions", notImplemented, interactive)
	admins.DELETE("/:id", notImplemented, interactive)
	admins.POST("/bulk", notImplemented, interactive)
	e.GET("/api/admin/roles", notImplemented, auth.RequireRole("admin"))

	e.GET("/invite/:token", notImplemented)
	e.POST("/api/invites/accept", auth.ProtectLoginAttempts(notImplemented))
	e.GET("/password-reset/:token", notImplemented)
	e.POST("/api/password-reset", auth.ProtectLoginAttempts(notImplemented))

	e.GET("/admin/users", func(c echo.Context) error {
		return renderAdmin(c, "users.html", "users")
	}, auth.RequireAuth, auth.RequireRole("admin"))
}

func init() {
	adminOnly := []string{"admin"}
	RegisterNav(
		NavItem{Key: "users", Section: "system", Label: "users", Icon: "i-users", Href: "/admin/users", Order: 80, Roles: adminOnly},
	)
	RegisterPaletteCommands(
		PaletteCommand{Key: "invite-user", Label: "inviteUser", Keywords: []string{"invite", "user", "邀請", "使用者"}, Icon: "i-user", Href: "/admin/users#invite", Roles: adminOnly},
	)
}
