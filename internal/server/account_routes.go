package server

import (
	"context"
	"fmt"
	"log"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/labstack/echo/v4"
)

// Account self-service, notifications and two-step verification
// (docs/tickets/06-accounts.md).
//
// Self-service API, auth.RequireAuth, every role. "I" marks interactive-only
// endpoints (auth.RequireInteractive).
//
//	S1   GET    /api/account/profile                     id, username, display_name, email, email_verified, pending_email, avatar_url, bio, locale, ui_theme, role, created_at, post_count, author_url
//	S2   PATCH  /api/account/profile                     display_name 1-120, bio <= 500, locale zh-TW|en, ui_theme; id/role/status/email/password ignored
//	S3   POST   /api/account/avatar                 I    multipart png|jpeg|webp <= 2 MB -> {avatar_url}
//	     DELETE /api/account/avatar                 I
//	S4   POST   /api/account/email                  I    {new_email, current_password} -> 202 {confirm_url?}; taken 409
//	     GET    /account/email/confirm?token=             public, one-time link (not under /api)
//	S5   POST   /api/account/password               I    {current_password?, new_password >= 8}; revokes other logins; OAuth-only accounts omit current_password
//	S6   GET    /api/account/2fa                          {enabled, enabled_at, recovery_remaining}
//	S7   POST   /api/account/2fa/enroll             I    -> {secret, otpauth_uri, qr_svg}; replaces an unverified secret
//	S8   POST   /api/account/2fa/verify             I    {code} -> enables, {recovery_codes:[10]} once
//	S9   POST   /api/account/2fa/disable            I    {current_password, code or recovery_code}
//	S10  POST   /api/account/2fa/recovery-codes     I    {code} -> 10 new codes, old ones void
//	S11  GET    /api/account/sessions                    [{id (16 chars), device_label, ip_masked, created_at, last_seen_at, current, new_device}], live only
//	S12  DELETE /api/account/sessions/:id           I    revoke one; another user's id 404
//	S13  POST   /api/account/sessions/revoke-others I    revoke all except the current login
//	S14-S18  personal API tokens (/api/account/tokens*) were removed by ticket 22;
//	         access keys on /api/agent/v1 replace them.
//	S19  GET    /api/account/notifications               category, unread=1, before, limit <= 50 -> {items, unread_count, counts}
//	S20  POST   /api/account/notifications/:id/read      own rows only; /api/account/notifications/read-all?category=
//	S21  GET|PUT /api/account/notification-preferences   {events, webhook_url (https, no private hosts), quiet}
//	S22  POST   /auth/login/2fa                     public  {mfa_token, code or recovery_code}; ProtectBrowserWrites and ProtectLoginAttempts
//
// Pages (renderAdmin): /admin/account?tab=profile|notifications|tokens|security|devices|identities,
// /admin/notifications.
//
// wireNotifications connects the comment hook and the bell count to the
// notify package.
func registerAccountRoutes(e *echo.Echo, renderAdmin func(c echo.Context, file, active string) error) {
	self := e.Group("/api/account", auth.RequireAuth, noStoreAccountAPI)
	interactive := auth.RequireInteractive()

	self.GET("/profile", getAccountProfile)
	self.PATCH("/profile", patchAccountProfile)
	self.POST("/avatar", uploadAccountAvatar, interactive)
	self.DELETE("/avatar", deleteAccountAvatar, interactive)
	self.POST("/email", requestAccountEmailChange, interactive)
	self.POST("/password", changeAccountPassword, interactive)

	self.GET("/2fa", accountTOTPStatus)
	self.POST("/2fa/enroll", enrollAccountTOTP, interactive)
	self.POST("/2fa/verify", verifyAccountTOTP, interactive)
	self.POST("/2fa/disable", disableAccountTOTP, interactive)
	self.POST("/2fa/recovery-codes", replaceAccountTOTPRecoveryCodes, interactive)

	self.GET("/sessions", listAccountSessions)
	self.DELETE("/sessions/:id", revokeAccountSession, interactive)
	self.POST("/sessions/revoke-others", revokeOtherAccountSessions, interactive)

	self.GET("/notifications", AccountNotifications)
	self.POST("/notifications/read-all", AccountNotificationReadAll)
	self.POST("/notifications/:id/read", AccountNotificationRead)
	self.GET("/notification-preferences", AccountNotificationPreferences)
	self.PUT("/notification-preferences", AccountNotificationPreferencesUpdate)

	e.GET("/account/email/confirm", noStoreAccountAPI(confirmAccountEmail))
	e.POST("/auth/login/2fa", noStoreAccountAPI(auth.ProtectBrowserWrites(auth.ProtectLoginAttempts(completeAccountMFALogin))))

	e.GET("/admin/account", noStoreAccountPage(auth.RequireAuth(staffPageGate(func(c echo.Context) error {
		return renderAdmin(c, "profile.html", "account")
	}))))
	e.GET("/admin/notifications", noStoreAccountPage(auth.RequireAuth(staffPageGate(func(c echo.Context) error {
		return renderAdmin(c, "notifications.html", "notifications")
	}))))
}

func noStoreAccountAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

// wireNotifications connects the comment hook and the bell count to the
// notify package. Called from RegisterRoutes; calling it again is harmless.
func wireNotifications() {
	OnCommentPending = func(comment core.Comment) {
		err := notify.Emit(context.Background(), data.DB, notify.Event{
			Kind:       notify.KindCommentPending,
			Category:   notify.CategoryComments,
			Params:     map[string]any{"comment_id": comment.ID, "post_id": comment.PostID},
			GroupKey:   fmt.Sprintf("comment.pending:%d", comment.PostID),
			Recipients: notify.Recipients{Roles: []string{"admin", "editor"}},
		})
		if err != nil {
			log.Printf("notify comment.pending: %v", err)
		}
	}
	UnreadNotifications = func(c echo.Context, userID uint) int {
		_ = c
		return notify.UnreadCount(data.DB, userID)
	}
}
