package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/labstack/echo/v4"
)

// handleSettingsList returns every option. Values of secret keys are opened
// and masked by settings.DisplayValue; nothing is returned in plaintext.
func handleSettingsList(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	var options []core.Option
	if err := data.DB.WithContext(c.Request().Context()).Find(&options).Error; err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	for i := range options {
		options[i].Value = settings.DisplayValue(options[i].Key, options[i].Value)
	}
	return c.JSON(http.StatusOK, options)
}

// handleSettingsWrite stores registered keys through settings.Set, which
// audits the write. Unregistered keys answer 400 and oversized values 413.
// The three maintenance keys keep ticket 05's validation and started_at
// stamping.
func handleSettingsWrite(c echo.Context) error {
	var req map[string]string
	if err := c.Bind(&req); err != nil {
		return err
	}
	maintenance := make(map[string]string, 3)
	for _, key := range []string{maintenanceEnabledKey, maintenanceMessageKey, maintenanceStartedKey} {
		if value, ok := req[key]; ok {
			maintenance[key] = value
			delete(req, key)
		}
	}
	var unknown []string
	for k := range req {
		if _, ok := settings.Lookup(k); !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown setting keys: " + strings.Join(unknown, ", ")})
	}
	if len(maintenance) > 0 {
		if data.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		if err := saveMaintenanceOptions(data.DB.WithContext(c.Request().Context()), maintenance); err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, errInvalidMaintenanceSetting) {
				status = http.StatusBadRequest
			}
			return c.JSON(status, map[string]string{"error": "Unable to save settings"})
		}
	}
	actor, _ := auth.GetUserFromContext(c)
	var actorID uint
	if actor != nil {
		actorID = actor.ID
	}
	if err := settings.Set(c.Request().Context(), actorID, req); err != nil {
		switch {
		case errors.Is(err, settings.ErrTooLarge):
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Setting value is too large"})
		case errors.Is(err, settings.ErrUnknownKey), errors.Is(err, settings.ErrInvalidValue):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Settings save failed"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Settings saved"})
}
