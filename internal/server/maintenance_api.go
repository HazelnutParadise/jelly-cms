package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// maintenanceMessageLimit caps the message in characters.
const maintenanceMessageLimit = 280

// registerMaintenanceRoutes mounts the maintenance admin API, admin only.
func registerMaintenanceRoutes(e *echo.Echo) {
	adminOnly := auth.RequireRole("admin")
	e.GET("/api/admin/maintenance", handleMaintenanceGet, adminOnly)
	e.PUT("/api/admin/maintenance", handleMaintenancePut, adminOnly)
}

// maintenanceRetryAfterMax caps retry_after in seconds (one day).
const maintenanceRetryAfterMax = 86400

// maintenanceRetryAfterKey stores the Retry-After value in seconds.
const maintenanceRetryAfterKey = "maintenance_retry_after"

// maintenanceState is what the admin sees of maintenance mode.
type maintenanceState struct {
	Enabled        bool   `json:"enabled"`
	Message        string `json:"message"`
	RetryAfter     int    `json:"retry_after"`
	ElapsedMinutes int    `json:"elapsed_minutes"`
}

// readMaintenanceState reads the maintenance options in one query. A
// retry_after that is not a number takes its registered default.
func readMaintenanceState(db *gorm.DB, now time.Time) (maintenanceState, error) {
	values := map[string]string{}
	keys := []string{maintenanceEnabledKey, maintenanceMessageKey, maintenanceStartedKey, maintenanceRetryAfterKey}
	var rows []core.Option
	if err := db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return maintenanceState{}, err
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	state := maintenanceState{
		Enabled: values[maintenanceEnabledKey] == "true",
		Message: values[maintenanceMessageKey],
	}
	retry, err := strconv.Atoi(values[maintenanceRetryAfterKey])
	if err != nil {
		retry, _ = strconv.Atoi(defaultOption(maintenanceRetryAfterKey))
	}
	state.RetryAfter = retry
	if state.Enabled {
		if started, err := time.Parse(time.RFC3339, values[maintenanceStartedKey]); err == nil {
			if minutes := int(now.Sub(started) / time.Minute); minutes > 0 {
				state.ElapsedMinutes = minutes
			}
		}
	}
	return state, nil
}

// handleMaintenanceGet returns the maintenance state to the admin.
func handleMaintenanceGet(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	state, err := readMaintenanceState(data.DB.WithContext(c.Request().Context()), time.Now())
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, state)
}

// handleMaintenancePut switches maintenance mode, sets the message and sets
// retry_after. The message counts in characters and holds at most 280. A
// retry_after between 1 and 86400 is accepted. The switch itself is audited
// with the actor and the new state, never the message.
func handleMaintenancePut(c echo.Context) error {
	var body struct {
		Enabled    *bool   `json:"enabled"`
		Message    *string `json:"message"`
		RetryAfter *int    `json:"retry_after"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if body.Enabled == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "enabled is required"})
	}
	if body.Message != nil && utf8.RuneCountInString(*body.Message) > maintenanceMessageLimit {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Message is longer than 280 characters"})
	}
	if body.RetryAfter != nil && (*body.RetryAfter < 1 || *body.RetryAfter > maintenanceRetryAfterMax) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "retry_after must be between 1 and 86400"})
	}
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}

	ctx := c.Request().Context()
	db := data.DB.WithContext(ctx)
	supplied := map[string]string{maintenanceEnabledKey: strconv.FormatBool(*body.Enabled)}
	if body.Message != nil {
		supplied[maintenanceMessageKey] = *body.Message
	}
	if err := saveMaintenanceOptions(db, supplied); err != nil {
		if errors.Is(err, errInvalidMaintenanceSetting) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Unable to save settings"})
	}

	actor, _ := auth.GetUserFromContext(c)
	var actorID uint
	if actor != nil {
		actorID = actor.ID
	}
	if body.RetryAfter != nil {
		if err := settings.Set(ctx, actorID, map[string]string{maintenanceRetryAfterKey: strconv.Itoa(*body.RetryAfter)}); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Settings save failed"})
		}
	}
	syslog.Audit(ctx, actorID, "maintenance.update", map[string]any{"enabled": *body.Enabled})

	state, err := readMaintenanceState(db, time.Now())
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, state)
}
