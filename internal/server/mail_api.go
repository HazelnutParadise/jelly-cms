package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/labstack/echo/v4"
)

var mailSettingKeys = []string{
	"mail_host", "mail_port", "mail_user", "mail_password_enc", "mail_from", "mail_tls", "mail_enabled",
}

// registerMailRoutes mounts mail settings and the real delivery test route.
// The test route reports queued only after the configured active queue accepts it.
func registerMailRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/mail/settings", handleMailSettingsGet, admin)
	e.PUT("/api/admin/mail/settings", handleMailSettingsPut, admin)
	e.POST("/api/admin/mail/test", handleMailTest, auth.RequireInteractive(), admin)
}

func handleMailSettingsGet(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	ctx := c.Request().Context()
	out := make(map[string]string, len(mailSettingKeys))
	for _, key := range mailSettingKeys {
		out[key] = settings.DisplayValue(key, settings.Get(ctx, key))
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusOK, out)
}

func handleMailSettingsPut(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	var raw map[string]json.RawMessage
	if err := c.Bind(&raw); err != nil || raw == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	allowed := make(map[string]bool, len(mailSettingKeys))
	for _, key := range mailSettingKeys {
		allowed[key] = true
	}
	values := make(map[string]string, len(raw))
	for key, encoded := range raw {
		if !allowed[key] {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown mail setting: " + key})
		}
		value, err := mailSettingScalar(encoded)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + key})
		}
		if key == "mail_password_enc" && strings.HasPrefix(value, "****") {
			// A masked value is a display token, never a replacement secret.
			continue
		}
		values[key] = value
	}
	for key, value := range values {
		if err := validateMailSetting(key, value); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + key})
		}
	}
	actor, _ := auth.GetUserFromContext(c)
	var actorID uint
	if actor != nil {
		actorID = actor.ID
	}
	if err := settings.Set(c.Request().Context(), actorID, values); err != nil {
		switch {
		case errors.Is(err, settings.ErrTooLarge):
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Mail setting is too large"})
		case errors.Is(err, settings.ErrInvalidValue), errors.Is(err, settings.ErrUnknownKey):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid mail settings"})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Mail settings save failed"})
		}
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Mail settings saved"})
}

func mailSettingScalar(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(n, 10), nil
	default:
		return "", fmt.Errorf("expected string, integer or boolean")
	}
}

func validateMailSetting(key, value string) error {
	switch key {
	case "mail_port":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("port out of range")
		}
	case "mail_enabled":
		if value != "true" && value != "false" {
			return errors.New("invalid boolean")
		}
	case "mail_tls":
		if value != "none" && value != "starttls" && value != "tls" {
			return errors.New("invalid TLS mode")
		}
	case "mail_from":
		if value != "" {
			if _, err := mail.ParseAddress(value); err != nil {
				return errors.New("invalid sender address")
			}
		}
	}
	return nil
}

func handleMailTest(c echo.Context) error {
	var req struct {
		To string `json:"to"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.To))
	if err != nil || addr.Address != strings.TrimSpace(req.To) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "A valid recipient email is required"})
	}
	payload, err := json.Marshal(map[string]string{"to": addr.Address, "kind": "test"})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to prepare mail test"})
	}
	id, err := EnqueueSystemJob(c.Request().Context(), data.DB, queue.Job{
		Queue: queue.QueueMail, Kind: "mail.send", Payload: payload, MaxAttempts: queue.DefaultMaxAttempts,
	})
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Mail test could not be queued"})
	}
	return c.JSON(http.StatusAccepted, map[string]string{"state": "queued", "job_id": id})
}
