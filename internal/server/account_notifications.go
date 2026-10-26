package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const notificationPageSize = 20
const notificationMaxPageSize = 50

var notificationCategories = map[string]bool{
	notify.CategoryComments: true,
	notify.CategoryOrders:   true,
	notify.CategorySystem:   true,
	notify.CategorySecurity: true,
}

// AccountNotifications implements S19. Counts always describe the complete
// visible, unexpired inbox even when the item list is filtered.
func AccountNotifications(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if data.DB == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "notifications are unavailable")
	}

	category := c.QueryParam("category")
	if category == "all" {
		category = ""
	}
	if category != "" && !notificationCategories[category] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid notification category")
	}
	limit := notificationPageSize
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > notificationMaxPageSize {
			return echo.NewHTTPError(http.StatusBadRequest, "limit must be between 1 and 50")
		}
		limit = parsed
	}
	unreadParam := c.QueryParam("unread")
	if unreadParam != "" && unreadParam != "0" && unreadParam != "1" {
		return echo.NewHTTPError(http.StatusBadRequest, "unread must be 0 or 1")
	}
	unreadOnly := unreadParam == "1"

	now := time.Now().UTC()
	base := func() *gorm.DB {
		return notify.VisibleForUser(data.DB.WithContext(c.Request().Context()), user.ID, now)
	}
	counts := map[string]int{"all": 0, "unread": 0, notify.CategoryComments: 0, notify.CategoryOrders: 0, notify.CategorySystem: 0, notify.CategorySecurity: 0}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count notifications")
	}
	counts["all"] = int(total)
	var unread int64
	if err := base().Where("read_at IS NULL").Count(&unread).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count notifications")
	}
	counts["unread"] = int(unread)
	for category := range notificationCategories {
		var categoryCount int64
		if err := base().Where("category = ?", category).Count(&categoryCount).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not count notifications")
		}
		counts[category] = int(categoryCount)
	}

	query := base()
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if unreadOnly {
		query = query.Where("read_at IS NULL")
	}
	if before := c.QueryParam("before"); before != "" {
		if id, err := strconv.ParseUint(before, 10, 64); err == nil && id > 0 {
			query = query.Where("id < ?", id)
		} else if timestamp, err := time.Parse(time.RFC3339Nano, before); err == nil {
			query = query.Where("created_at < ?", timestamp)
		} else {
			return echo.NewHTTPError(http.StatusBadRequest, "before must be a notification ID or RFC3339 timestamp")
		}
	}
	var items []core.Notification
	if err := query.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load notifications")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"items": items, "unread_count": counts["unread"], "counts": counts,
	})
}

// AccountNotificationRead implements the single-row part of S20.
func AccountNotificationRead(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if data.DB == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "notifications are unavailable")
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid notification ID")
	}
	var notification core.Notification
	err = notify.VisibleForUser(data.DB.WithContext(c.Request().Context()), user.ID, time.Now().UTC()).
		Where("id = ?", id).
		First(&notification).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "notification not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load notification")
	}
	if notification.ReadAt == nil {
		if err := data.DB.WithContext(c.Request().Context()).Model(&core.Notification{}).
			Where("id = ? AND user_id = ? AND read_at IS NULL", notification.ID, user.ID).
			Update("read_at", time.Now().UTC()).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not mark notification read")
		}
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// AccountNotificationReadAll implements the bulk-read part of S20.
func AccountNotificationReadAll(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if data.DB == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "notifications are unavailable")
	}
	category := c.QueryParam("category")
	if category == "all" {
		category = ""
	}
	if category != "" && !notificationCategories[category] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid notification category")
	}
	query := notify.VisibleForUser(data.DB.WithContext(c.Request().Context()), user.ID, time.Now().UTC()).
		Where("read_at IS NULL")
	if category != "" {
		query = query.Where("category = ?", category)
	}
	result := query.Update("read_at", time.Now().UTC())
	if result.Error != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mark notifications read")
	}
	return c.JSON(http.StatusOK, map[string]int64{"updated": result.RowsAffected})
}

type notificationPreference struct {
	InApp   bool `json:"in_app"`
	Email   bool `json:"email"`
	Webhook bool `json:"webhook"`
}

type notificationQuiet struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
	TZ      string `json:"tz"`
}

type notificationPreferencesResponse struct {
	Events     map[string]notificationPreference `json:"events"`
	WebhookURL string                            `json:"webhook_url"`
	Quiet      notificationQuiet                 `json:"quiet"`
}

// AccountNotificationPreferences implements the read side of S21.
func AccountNotificationPreferences(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if data.DB == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "notification preferences are unavailable")
	}
	response := notificationPreferencesResponse{
		Events: make(map[string]notificationPreference),
	}
	for _, event := range notify.PreferenceEvents() {
		response.Events[event] = notificationPreference{InApp: true}
	}
	var settings core.NotificationSettings
	err = data.DB.WithContext(c.Request().Context()).Where("user_id = ?", user.ID).First(&settings).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load notification settings")
	}
	response.WebhookURL = settings.WebhookURL
	response.Quiet = notificationQuiet{
		Enabled: settings.QuietStart != "" && settings.QuietEnd != "" && settings.QuietTZ != "",
		Start:   settings.QuietStart, End: settings.QuietEnd, TZ: settings.QuietTZ,
	}
	var prefs []core.NotificationPref
	if err := data.DB.WithContext(c.Request().Context()).Where("user_id = ?", user.ID).Find(&prefs).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load notification preferences")
	}
	for _, pref := range prefs {
		if _, ok := response.Events[pref.Event]; ok {
			response.Events[pref.Event] = notificationPreference{InApp: pref.InApp, Email: pref.Email, Webhook: pref.Webhook}
		}
	}
	return c.JSON(http.StatusOK, response)
}

type notificationPreferenceInput struct {
	InApp   *bool `json:"in_app"`
	Email   *bool `json:"email"`
	Webhook *bool `json:"webhook"`
}

type notificationQuietInput struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
	TZ      string `json:"tz"`
}

type notificationPreferencesInput struct {
	Events     map[string]notificationPreferenceInput `json:"events"`
	WebhookURL string                                 `json:"webhook_url"`
	Quiet      notificationQuietInput                 `json:"quiet"`
}

// AccountNotificationPreferencesUpdate implements the write side of S21.
func AccountNotificationPreferencesUpdate(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if data.DB == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "notification preferences are unavailable")
	}
	var input notificationPreferencesInput
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid notification preferences")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid notification preferences")
	}

	defaults := make(map[string]notificationPreference, len(notify.PreferenceEvents()))
	for _, event := range notify.PreferenceEvents() {
		defaults[event] = notificationPreference{InApp: true}
	}
	for event, pref := range input.Events {
		current, ok := defaults[event]
		if !ok {
			return echo.NewHTTPError(http.StatusBadRequest, "unknown notification event")
		}
		if pref.InApp != nil {
			current.InApp = *pref.InApp
		}
		if pref.Email != nil {
			current.Email = *pref.Email
		}
		if pref.Webhook != nil {
			current.Webhook = *pref.Webhook
		}
		defaults[event] = current
	}

	input.WebhookURL = strings.TrimSpace(input.WebhookURL)
	if len(input.WebhookURL) > 2048 {
		return echo.NewHTTPError(http.StatusBadRequest, "webhook_url is too long")
	}
	webhookEnabled := false
	for _, pref := range defaults {
		webhookEnabled = webhookEnabled || pref.Webhook
	}
	if webhookEnabled && input.WebhookURL == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "webhook_url is required when webhook notifications are enabled")
	}
	if err := validateNotificationWebhook(c.Request().Context(), input.WebhookURL); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "webhook_url must use HTTPS and resolve only to public addresses")
	}

	settings := core.NotificationSettings{UserID: user.ID, WebhookURL: input.WebhookURL}
	if input.Quiet.Enabled {
		start, err := parseNotificationClock(input.Quiet.Start)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "quiet.start must use HH:MM")
		}
		end, err := parseNotificationClock(input.Quiet.End)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "quiet.end must use HH:MM")
		}
		if _, err := time.LoadLocation(input.Quiet.TZ); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "quiet.tz must be a valid time zone")
		}
		settings.QuietStart = start
		settings.QuietEnd = end
		settings.QuietTZ = input.Quiet.TZ
	} else if input.Quiet.Start != "" || input.Quiet.End != "" || input.Quiet.TZ != "" {
		return echo.NewHTTPError(http.StatusBadRequest, "disabled quiet hours must not include a time or time zone")
	}

	rows := make([]core.NotificationPref, 0, len(defaults))
	for _, event := range notify.PreferenceEvents() {
		pref := defaults[event]
		rows = append(rows, core.NotificationPref{UserID: user.ID, Event: event, InApp: pref.InApp, Email: pref.Email, Webhook: pref.Webhook})
	}
	if err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"webhook_url", "quiet_start", "quiet_end", "quiet_tz"}),
		}).Create(&settings).Error; err != nil {
			return fmt.Errorf("save notification settings: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "event"}},
			DoUpdates: clause.AssignmentColumns([]string{"in_app", "email", "webhook"}),
		}).Create(&rows).Error; err != nil {
			return fmt.Errorf("save notification preferences: %w", err)
		}
		return nil
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save notification preferences")
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func parseNotificationClock(value string) (string, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return "", fmt.Errorf("invalid HH:MM time")
	}
	return value, nil
}

func validateNotificationWebhook(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("webhook URL must be an HTTPS URL without credentials or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("webhook host is local")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicNotificationIP(ip) {
			return fmt.Errorf("webhook host is not public")
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		var dnsErr *net.DNSError
		// An endpoint can be saved before its DNS record is created. No address
		// can be contacted now; delivery must resolve and validate again later.
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil
		}
		return fmt.Errorf("resolve webhook host: %w", err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("webhook host has no addresses")
	}
	for _, address := range addresses {
		if !isPublicNotificationIP(address.IP) {
			return fmt.Errorf("webhook host resolves to a non-public address")
		}
	}
	return nil
}

var notificationNonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublicNotificationIP(ip net.IP) bool {
	addr, err := netip.ParseAddr(ip.String())
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range notificationNonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
