package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maintenanceRetryAfter = 600

const (
	maintenanceEnabledKey  = "maintenance_enabled"
	maintenanceMessageKey  = "maintenance_message"
	maintenanceStartedKey  = "maintenance_started_at"
	maintenanceTimezoneKey = "site_timezone"
)

var errInvalidMaintenanceSetting = errors.New("invalid maintenance setting")

func maintenanceMiddleware(tm *theme.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.EscapedPath()
			if maintenanceBypass(path, nil) {
				return next(c)
			}
			db := data.DB
			if db == nil {
				return next(c)
			}
			db = db.WithContext(c.Request().Context())
			var rows []core.Option
			keys := []string{maintenanceEnabledKey, maintenanceMessageKey, maintenanceStartedKey, maintenanceRetryAfterKey, maintenanceTimezoneKey}
			if err := db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
			}
			values := make(map[string]string, len(rows))
			for _, row := range rows {
				values[row.Key] = row.Value
			}
			if values[maintenanceEnabledKey] != "true" {
				return next(c)
			}

			user, err := auth.GetCurrentUser(c)
			if err == nil && maintenanceBypass(path, user) {
				return next(c)
			}

			retryAfter := maintenanceRetryAfter
			if n, err := strconv.Atoi(values[maintenanceRetryAfterKey]); err == nil && n >= 1 && n <= maintenanceRetryAfterMax {
				retryAfter = n
			}
			c.Response().Header().Set(echo.HeaderRetryAfter, strconv.Itoa(retryAfter))
			c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
			if isAPIPath(path) {
				return c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
					"error": "Service unavailable",
					"code":  http.StatusServiceUnavailable,
				})
			}

			location := time.Local
			if timezone := strings.TrimSpace(values[maintenanceTimezoneKey]); timezone != "" {
				if configured, loadErr := time.LoadLocation(timezone); loadErr == nil {
					location = configured
				}
			}
			startedAt := time.Now()
			if raw := values[maintenanceStartedKey]; raw != "" {
				parsed, parseErr := time.Parse(time.RFC3339, raw)
				if parseErr != nil {
					log.Printf("parse maintenance start time: %v", parseErr)
				} else {
					startedAt = parsed
				}
			}
			startedAt = startedAt.In(location)
			elapsed := int(time.Since(startedAt) / time.Minute)
			if elapsed < 0 {
				elapsed = 0
			}
			siteTitle, err := publicSiteTitle(db)
			if err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
			}
			if tm == nil {
				return c.String(http.StatusServiceUnavailable, "Service unavailable")
			}
			body, err := renderPublicThemePage(c, tm, db, "maintenance.html", map[string]interface{}{
				"SiteTitle":      siteTitle,
				"CurrentPath":    path,
				"Message":        values[maintenanceMessageKey],
				"StartedAt":      startedAt,
				"ElapsedMinutes": elapsed,
				"RetryAfter":     retryAfter,
			})
			if err != nil {
				log.Printf("render maintenance page: %v", err)
				return c.String(http.StatusServiceUnavailable, "Service unavailable")
			}
			return c.HTMLBlob(http.StatusServiceUnavailable, body)
		}
	}
}

func maintenanceBypass(path string, user *core.User) bool {
	if path == "/login" ||
		pathOrSubtree(path, machineAPIBasePath) ||
		pathOrSubtree(path, "/auth") ||
		pathOrSubtree(path, "/admin") ||
		pathOrSubtree(path, "/api/admin") ||
		pathOrSubtree(path, "/api/auth") ||
		pathOrSubtree(path, "/install") ||
		pathOrSubtree(path, "/uploads") ||
		pathOrSubtree(path, "/admin/assets") ||
		path == "/api/oauth/status" {
		return true
	}
	return user != nil && (user.Role == "admin" || user.Role == "editor")
}

func pathOrSubtree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

func setMaintenance(db *gorm.DB, enabled bool, message string) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return saveMaintenanceOptions(db, map[string]string{
		maintenanceEnabledKey: value,
		maintenanceMessageKey: message,
	})
}

func saveMaintenanceOptions(db *gorm.DB, supplied map[string]string) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	for key, value := range supplied {
		switch key {
		case maintenanceEnabledKey:
			if value != "true" && value != "false" {
				return fmt.Errorf("%w: maintenance_enabled must be true or false", errInvalidMaintenanceSetting)
			}
		case maintenanceMessageKey:
		case maintenanceStartedKey:
			if value != "" {
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return fmt.Errorf("%w: maintenance_started_at must be RFC3339", errInvalidMaintenanceSetting)
				}
			}
		default:
			return fmt.Errorf("%w: unsupported maintenance setting", errInvalidMaintenanceSetting)
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		enabled, err := lockMaintenanceOption(tx, maintenanceEnabledKey, "false")
		if err != nil {
			return err
		}
		started, err := lockMaintenanceOption(tx, maintenanceStartedKey, "")
		if err != nil {
			return err
		}

		enabledValue := enabled.Value
		if value, ok := supplied[maintenanceEnabledKey]; ok {
			enabledValue = value
			if err := writeMaintenanceOption(tx, maintenanceEnabledKey, enabledValue); err != nil {
				return err
			}
		}
		if enabledValue != "true" {
			if err := writeMaintenanceOption(tx, maintenanceStartedKey, ""); err != nil {
				return err
			}
		} else if value, explicit := supplied[maintenanceStartedKey]; explicit {
			if err := writeMaintenanceOption(tx, maintenanceStartedKey, value); err != nil {
				return err
			}
		} else if value, explicit := supplied[maintenanceEnabledKey]; explicit && value == "true" {
			if enabled.Value != "true" || started.Value == "" {
				if err := writeMaintenanceOption(tx, maintenanceStartedKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
					return err
				}
			}
		}
		if value, explicit := supplied[maintenanceMessageKey]; explicit {
			return writeMaintenanceOption(tx, maintenanceMessageKey, value)
		}
		return nil
	})
}

func lockMaintenanceOption(tx *gorm.DB, key, defaultValue string) (core.Option, error) {
	row := core.Option{Key: key, Value: defaultValue}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoNothing: true,
	}).Create(&row).Error; err != nil {
		return core.Option{}, err
	}
	var current core.Option
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", key).First(&current).Error
	return current, err
}

func writeMaintenanceOption(tx *gorm.DB, key, value string) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"value": value}),
	}).Create(&core.Option{Key: key, Value: value}).Error
}
