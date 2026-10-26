package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	if err := data.DB.WithContext(c.Request().Context()).
		Where("key NOT LIKE ? ESCAPE E'\\\\' AND key <> ?", indexNowPendingOptionLikePattern(), queueSwitchJournalKey).
		Find(&options).Error; err != nil {
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
	var raw map[string]json.RawMessage
	if err := c.Bind(&raw); err != nil || raw == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid settings request"})
	}
	req := make(map[string]string, len(raw))
	for key, value := range raw {
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			req[key] = text
			continue
		}
		var typed any
		decoder := json.NewDecoder(strings.NewReader(string(value)))
		decoder.UseNumber()
		if err := decoder.Decode(&typed); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid setting value"})
		}
		switch v := typed.(type) {
		case bool:
			req[key] = strconv.FormatBool(v)
		case json.Number:
			n, err := v.Int64()
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "Setting numbers must be integers"})
			}
			req[key] = strconv.FormatInt(n, 10)
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Setting values must be strings, booleans or integers"})
		}
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
		if strings.HasPrefix(k, "inject_") || k == "queue_driver" || k == "queue_url_enc" || k == "cache_driver" || k == "cache_url_enc" {
			unknown = append(unknown, k)
			continue
		}
		spec, ok := settings.Lookup(k)
		if !ok || (spec.Tab == "internal" && k != maintenanceStartedKey) {
			unknown = append(unknown, k)
			continue
		}
		limit := spec.MaxBytes
		if limit <= 0 {
			limit = settings.DefaultMaxBytes
		}
		if len(req[k]) > limit {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Setting value is too large"})
		}
		if err := validateRegisteredSetting(spec.Kind, req[k]); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + k})
		}
		if err := validateSettingValue(k, req[k]); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + k})
		}
	}
	for k, value := range maintenance {
		if k == maintenanceMessageKey && len([]rune(value)) > maintenanceMessageLimit {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + k})
		}
		if k == maintenanceEnabledKey && value != "true" && value != "false" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid value for " + k})
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

func validateRegisteredSetting(kind, value string) error {
	if value == "" {
		return nil
	}
	switch kind {
	case settings.KindInt:
		_, err := strconv.Atoi(value)
		return err
	case settings.KindBool:
		if value != "true" && value != "false" {
			return errors.New("invalid boolean")
		}
	case settings.KindJSON:
		if !json.Valid([]byte(value)) {
			return errors.New("invalid JSON")
		}
	}
	return nil
}

// validateSettingValue applies the structured constraints used by public
// routes before any settings in the request are written.
func validateSettingValue(key, value string) error {
	switch key {
	case "site_timezone":
		_, err := time.LoadLocation(value)
		return err
	case "seo_snippet_mode":
		if value != "default" && value != "short" && value != "noarchive" {
			return errors.New("unknown snippet mode")
		}
	case "ai_policy_preset":
		if value != "block_training_allow_search" && value != "block_all" && value != "allow_all" {
			return errors.New("unknown AI policy")
		}
	case "seo_index_types":
		var types map[string]bool
		if err := json.Unmarshal([]byte(value), &types); err != nil || types == nil {
			return errors.New("expected object of boolean index policies")
		}
		for key := range types {
			if !map[string]bool{"post": true, "page": true, "home": true, "search": true, "archive": true, "category": true, "product": true, "tag": true, "author": true, "commerce": true, "account": true}[key] {
				return errors.New("unknown index type")
			}
		}
	case "ai_bots":
		var bots map[string]string
		if err := json.Unmarshal([]byte(value), &bots); err != nil || bots == nil {
			return errors.New("expected object of crawler policies")
		}
		for bot, action := range bots {
			if strings.TrimSpace(bot) == "" || strings.ContainsAny(bot, "\r\n:#") || (action != "allow" && action != "block") {
				return errors.New("invalid crawler policy")
			}
		}
	case "ai_custom_rules":
		var rules []struct {
			UserAgent string `json:"user_agent"`
			Action    string `json:"action"`
			Path      string `json:"path"`
		}
		if err := json.Unmarshal([]byte(value), &rules); err != nil || len(rules) > 256 {
			return errors.New("invalid crawler rules")
		}
		for _, rule := range rules {
			if strings.TrimSpace(rule.UserAgent) == "" || strings.ContainsAny(rule.UserAgent, "\r\n:#") ||
				(rule.Action != "allow" && rule.Action != "disallow") || !strings.HasPrefix(rule.Path, "/") || strings.ContainsAny(rule.Path, "\r\n#") {
				return errors.New("invalid crawler rule")
			}
		}
	}
	return nil
}
