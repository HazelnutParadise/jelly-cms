package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Option keys of the code injection slots (ticket 08b 「程式碼注入」).
const (
	keyInjectHeadAll     = "inject_head_all"
	keyInjectHeadContent = "inject_head_content"
	keyInjectFooter      = "inject_footer"
	keyInjectOnCommerce  = "inject_on_commerce"
)

// registerInjectionRoutes mounts the injection admin API. GET is admin only;
// PUT is admin only and interactive only (auth.RequireInteractive runs first).
func registerInjectionRoutes(e *echo.Echo) {
	adminOnly := auth.RequireRole("admin")
	e.GET("/api/admin/settings/injection", handleInjectionGet, adminOnly)
	e.PUT("/api/admin/settings/injection", handleInjectionPut, auth.RequireInteractive(), adminOnly)
}

// injectionMaxBytes caps each slot at 64 KiB.
const injectionMaxBytes = 64 << 10

// injectionBody is the PUT body. A slot left out is not changed.
type injectionBody struct {
	HeadAll         *string `json:"head_all"`
	HeadContent     *string `json:"head_content"`
	Footer          *string `json:"footer"`
	OnCommerce      *bool   `json:"on_commerce"`
	CurrentPassword string  `json:"current_password"`
}

// handleInjectionGet returns the three slots to the admin. The values are raw
// HTML, so the response must not be cached.
func handleInjectionGet(c echo.Context) error {
	ctx := c.Request().Context()
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusOK, map[string]string{
		"head_all":     settings.Get(ctx, keyInjectHeadAll),
		"head_content": settings.Get(ctx, keyInjectHeadContent),
		"footer":       settings.Get(ctx, keyInjectFooter),
		"on_commerce":  settings.Get(ctx, keyInjectOnCommerce),
	})
}

// handleInjectionPut stores the slots. It is interactive only, admin only, and
// needs the admin's current password. The audit row keeps the length and the
// sha256 of each slot written, never the content.
func handleInjectionPut(c echo.Context) error {
	actor, ok := auth.GetUserFromContext(c)
	if !ok || actor == nil || actor.Role != "admin" {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	var body injectionBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if body.CurrentPassword == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "current_password is required"})
	}
	if actor.Password == "" || bcrypt.CompareHashAndPassword([]byte(actor.Password), []byte(body.CurrentPassword)) != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Password does not match"})
	}

	type slot struct {
		name, key string
		value     *string
	}
	slots := []slot{
		{"head_all", keyInjectHeadAll, body.HeadAll},
		{"head_content", keyInjectHeadContent, body.HeadContent},
		{"footer", keyInjectFooter, body.Footer},
	}
	kv := map[string]string{}
	for _, s := range slots {
		if s.value == nil {
			continue
		}
		if len(*s.value) > injectionMaxBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Injection exceeds 64 KiB"})
		}
		kv[s.key] = *s.value
	}
	if body.OnCommerce != nil {
		kv[keyInjectOnCommerce] = strconv.FormatBool(*body.OnCommerce)
	}
	if len(kv) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "No injection slot supplied"})
	}

	ctx := c.Request().Context()
	if err := settings.Set(ctx, actor.ID, kv); err != nil {
		switch {
		case errors.Is(err, settings.ErrTooLarge):
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Injection exceeds 64 KiB"})
		case errors.Is(err, settings.ErrInvalidValue), errors.Is(err, settings.ErrUnknownKey):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid injection"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Injection save failed"})
	}
	for _, s := range slots {
		if s.value == nil {
			continue
		}
		sum := sha256.Sum256([]byte(*s.value))
		syslog.Audit(ctx, actor.ID, "injection.update", map[string]any{
			"slot":   s.name,
			"length": len(*s.value),
			"sha256": hex.EncodeToString(sum[:]),
		})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Injection saved"})
}
