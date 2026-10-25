package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Plugin detail API (docs/tickets/08-system.md, 08d). Plugin files live in
// data/plugins/<id>; the state (enabled flag, settings, snapshots, extension
// bindings) lives in the tables created by migration 0021.
//
// Interactive-only rule: uninstall is interactive only. It uses the same rule
// as injection_api.go (isInteractiveRequest), so a Bearer token never passes.

const (
	pluginDirRoot         = "data/plugins"
	pluginSnapshotKeep    = 3
	pluginLogLimit        = 200
	pluginSettingsMaxJSON = 64 << 10
	pluginSecretSealing   = "plugin:"
	extensionBuiltinID    = "builtin"
	extensionMaxProviders = 16
	extensionIDMaxLen     = 128
)

var (
	errPluginNotFound = errors.New("plugin not found")
	errPluginFiles    = errors.New("plugin files are missing or invalid")
)

// pluginRow mirrors one row of the plugins table.
type pluginRow struct {
	ID          string    `gorm:"column:id"`
	Kind        string    `gorm:"column:kind"`
	Version     string    `gorm:"column:version"`
	Source      string    `gorm:"column:source"`
	Enabled     bool      `gorm:"column:enabled"`
	Signature   string    `gorm:"column:signature"`
	InstalledAt time.Time `gorm:"column:installed_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// pluginSettingsRow mirrors one row of plugin_settings.
type pluginSettingsRow struct {
	Values     []byte `gorm:"column:values"`
	SecretsEnc []byte `gorm:"column:secrets_enc"`
	Version    int    `gorm:"column:version"`
}

// pluginDetailAPI holds the plugin runtime. The runtime may be nil when the
// database is not ready or when a test builds the router without one.
type pluginDetailAPI struct {
	runtime *plugin.Runtime
}

// registerPluginDetailRoutes mounts the plugin detail, settings, lifecycle,
// action and extension endpoints. Every route is admin only.
func registerPluginDetailRoutes(e *echo.Echo, runtime *plugin.Runtime) {
	api := &pluginDetailAPI{runtime: runtime}
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/plugins/:id", api.detail, admin)
	e.POST("/api/admin/plugins/:id/enable", api.enable, admin)
	e.POST("/api/admin/plugins/:id/disable", api.disable, admin)
	e.DELETE("/api/admin/plugins/:id", api.uninstall, interactiveOnly, admin)
	e.GET("/api/admin/plugins/:id/settings", api.getSettings, admin)
	e.PUT("/api/admin/plugins/:id/settings", api.putSettings, admin)
	e.POST("/api/admin/plugins/:id/settings/reset", api.resetSettings, admin)
	e.POST("/api/admin/plugins/:id/test", api.test, admin)
	e.POST("/api/admin/plugins/:id/actions/:action", api.action, admin)
	e.GET("/api/admin/plugins/:id/logs", api.logs, admin)
	e.GET("/api/admin/extensions", api.listExtensions, admin)
	e.PUT("/api/admin/extensions/:point", api.putExtension, admin)
}

// interactiveOnly answers 403 unless the request comes from a browser session.
func interactiveOnly(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !isInteractiveRequest(c) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "Interactive browser session required"})
		}

		return next(c)
	}
}

// PluginEnabledFilter decides which plugin directories load at start-up. A
// plugin whose row says disabled stays unloaded. A directory without a row
// keeps loading, as it did before the plugins table existed.
func PluginEnabledFilter() func(pluginID string) bool {
	return func(pluginID string) bool {
		var rows []struct {
			Enabled bool
		}
		if err := data.DB.Table("plugins").Select("enabled").Where("id = ?", pluginID).Find(&rows).Error; err != nil {
			log.Printf("plugin %s: read enabled flag: %v", pluginID, err)

			return false
		}
		if len(rows) == 0 {
			return true
		}

		return rows[0].Enabled
	}
}

// findPluginRow returns the plugins row for id.
func findPluginRow(id string) (pluginRow, error) {
	if !plugin.ValidPluginID(id) {
		return pluginRow{}, errPluginNotFound
	}

	var row pluginRow
	err := data.DB.Table("plugins").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pluginRow{}, errPluginNotFound
	}

	return row, err
}

// findPlugin returns the row and the manifest of id. A missing or mismatched
// manifest is errPluginFiles, because the row then describes nothing on disk.
func findPlugin(id string) (pluginRow, plugin.PluginMetadata, error) {
	row, err := findPluginRow(id)
	if err != nil {
		return pluginRow{}, plugin.PluginMetadata{}, err
	}

	meta, err := plugin.ReadManifest(filepath.Join(pluginDirRoot, id))
	if err != nil || meta.ID != id {
		return pluginRow{}, plugin.PluginMetadata{}, errPluginFiles
	}

	return row, meta, nil
}

// pluginFailure maps a lookup error to a response. Unexpected errors are logged
// and hidden behind a generic message.
func pluginFailure(c echo.Context, err error) error {
	switch {
	case errors.Is(err, errPluginNotFound):
		return infraError(c, http.StatusNotFound, "Plugin not found")
	case errors.Is(err, errPluginFiles):
		return infraError(c, http.StatusConflict, "Plugin files are missing or invalid")
	default:
		log.Printf("plugin request failed: %v", err)

		return infraError(c, http.StatusInternalServerError, "Plugin request failed")
	}
}

// pluginActorID returns the id of the signed-in admin, or 0.
func pluginActorID(c echo.Context) uint {
	if user, ok := auth.GetUserFromContext(c); ok && user != nil {
		return user.ID
	}

	return 0
}

type pluginPermissionView struct {
	Name     string `json:"name"`
	Enforced bool   `json:"enforced"`
}

type pluginDetailView struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Source      string                 `json:"source"`
	Signature   string                 `json:"signature"`
	Enabled     bool                   `json:"enabled"`
	Loaded      bool                   `json:"loaded"`
	Settings    []plugin.SettingSpec   `json:"settings"`
	Permissions []pluginPermissionView `json:"permissions"`
	Actions     []plugin.ActionSpec    `json:"actions"`
	Extensions  []plugin.ExtensionSpec `json:"extensions"`
	InstalledAt time.Time              `json:"installed_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

func (a *pluginDetailAPI) loaded(id string) bool {
	return a.runtime != nil && a.runtime.IsLoaded(id)
}

// detail returns the manifest, the stored state and the runtime state of a
// plugin. It works for plugins that are disabled or not running.
func (a *pluginDetailAPI) detail(c echo.Context) error {
	row, meta, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	permissions := make([]pluginPermissionView, 0, len(meta.Permissions))
	for _, permission := range meta.Permissions {
		permissions = append(permissions, pluginPermissionView{Name: permission.Name, Enforced: permission.Enforced()})
	}

	return c.JSON(http.StatusOK, pluginDetailView{
		ID:          row.ID,
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		Author:      meta.Author,
		Source:      row.Source,
		Signature:   row.Signature,
		Enabled:     row.Enabled,
		Loaded:      a.loaded(row.ID),
		Settings:    nonNilSlice(meta.Settings),
		Permissions: permissions,
		Actions:     nonNilSlice(meta.Actions),
		Extensions:  nonNilSlice(meta.Extensions),
		InstalledAt: row.InstalledAt,
		UpdatedAt:   row.UpdatedAt,
	})
}

// nonNilSlice keeps empty lists as [] in JSON instead of null.
func nonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}

	return items
}

// enable starts the plugin and stores the flag. A plugin that fails to start
// stays disabled and the response says so.
func (a *pluginDetailAPI) enable(c echo.Context) error {
	ctx := c.Request().Context()
	row, _, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	startedHere := false
	if a.runtime != nil && !a.loaded(row.ID) {
		if err := a.runtime.Load(ctx, filepath.Join(pluginDirRoot, row.ID)); err != nil {
			log.Printf("plugin %s: start failed: %v", row.ID, err)

			return infraError(c, http.StatusConflict, "Plugin failed to start")
		}
		startedHere = true
	}

	if err := data.DB.Exec(`UPDATE plugins SET enabled = TRUE, updated_at = now() WHERE id = ?`, row.ID).Error; err != nil {
		if startedHere {
			_ = a.runtime.Unload(ctx, row.ID)
		}

		return pluginFailure(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.enable", map[string]any{"plugin": row.ID})

	return c.JSON(http.StatusOK, map[string]any{"id": row.ID, "enabled": true, "loaded": a.loaded(row.ID)})
}

// disable stores the flag and unloads the plugin. The flag is written first, so
// a failed unload never leaves a plugin that restarts enabled.
func (a *pluginDetailAPI) disable(c echo.Context) error {
	ctx := c.Request().Context()
	row, err := findPluginRow(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	if err := data.DB.Exec(`UPDATE plugins SET enabled = FALSE, updated_at = now() WHERE id = ?`, row.ID).Error; err != nil {
		return pluginFailure(c, err)
	}

	if a.loaded(row.ID) {
		if err := a.runtime.Unload(ctx, row.ID); err != nil {
			log.Printf("plugin %s: unload after disable: %v", row.ID, err)
		}
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.disable", map[string]any{"plugin": row.ID})

	return c.JSON(http.StatusOK, map[string]any{"id": row.ID, "enabled": false, "loaded": a.loaded(row.ID)})
}

// uninstall removes a plugin. keep_settings defaults to true: the settings,
// snapshots and plugin data stay so a reinstall finds them again. With false they
// are deleted with the plugin.
func (a *pluginDetailAPI) uninstall(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	row, err := findPluginRow(id)
	if err != nil {
		return pluginFailure(c, err)
	}

	keep := true
	if raw := c.QueryParam("keep_settings"); raw != "" {
		keep, err = strconv.ParseBool(raw)
		if err != nil {
			return infraError(c, http.StatusBadRequest, "keep_settings must be true or false")
		}
	}
	if a.loaded(row.ID) {
		if err := a.runtime.Unload(ctx, row.ID); err != nil {
			log.Printf("plugin %s: unload before uninstall: %v", row.ID, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(pluginDirRoot, row.ID)); err != nil {
		log.Printf("plugin %s: remove files: %v", row.ID, err)

		return infraError(c, http.StatusInternalServerError, "Plugin files could not be removed")
	}

	err = data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM extension_bindings WHERE provider_id = ?`, row.ID).Error; err != nil {
			return err
		}

		if !keep {
			for _, table := range []string{"plugin_settings", "plugin_setting_snapshots", "plugin_data"} {
				if err := tx.Exec(`DELETE FROM `+table+` WHERE plugin_id = ?`, row.ID).Error; err != nil {
					return err
				}
			}
		}

		return tx.Exec(`DELETE FROM plugins WHERE id = ?`, row.ID).Error
	})
	if err != nil {
		return pluginFailure(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.uninstall", map[string]any{"plugin": row.ID, "keep_settings": keep})

	return c.JSON(http.StatusOK, map[string]any{"removed": row.ID})
}

// readSettingsRow returns the stored values, the decrypted secrets and the
// version. A plugin without a settings row has empty values and version 0.
func readSettingsRow(tx *gorm.DB, id string) (map[string]any, map[string]string, int, error) {
	var rows []pluginSettingsRow
	err := tx.Table("plugin_settings").Select(`"values", secrets_enc, version`).Where("plugin_id = ?", id).Find(&rows).Error
	if err != nil {
		return nil, nil, 0, err
	}

	plain := map[string]any{}
	secrets := map[string]string{}
	if len(rows) == 0 {
		return plain, secrets, 0, nil
	}

	if len(rows[0].Values) > 0 {
		if err := json.Unmarshal(rows[0].Values, &plain); err != nil {
			return nil, nil, 0, fmt.Errorf("decode settings of %s: %w", id, err)
		}
	}
	if len(rows[0].SecretsEnc) > 0 {
		opened, err := settings.OpenBytes(pluginSecretSealing+id, rows[0].SecretsEnc)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("open secrets of %s: %w", id, err)
		}
		if err := json.Unmarshal(opened, &secrets); err != nil {
			return nil, nil, 0, fmt.Errorf("decode secrets of %s: %w", id, err)
		}
	}

	return plain, secrets, rows[0].Version, nil
}

// settingsView is the admin view of the stored settings. Secrets show only
// whether they are set and their last four characters.
func settingsView(specs []plugin.SettingSpec, plain map[string]any, secrets map[string]string) map[string]any {
	view := make(map[string]any, len(specs))
	for _, spec := range specs {
		if spec.Type == plugin.SettingSecret {
			if value := secrets[spec.Key]; value != "" {
				view[spec.Key] = map[string]any{"set": true, "source": "stored", "masked": plugin.MaskSecret(value)}
			} else {
				view[spec.Key] = map[string]any{"set": false, "source": "unset"}
			}

			continue
		}

		if value, ok := plain[spec.Key]; ok {
			view[spec.Key] = value
		} else if spec.Default != nil {
			view[spec.Key] = spec.Default
		} else {
			view[spec.Key] = nil
		}
	}

	return view
}

// getSettings returns the declared settings with their current values.
func (a *pluginDetailAPI) getSettings(c echo.Context) error {
	_, meta, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	plain, secrets, version, err := readSettingsRow(data.DB, meta.ID)
	if err != nil {
		return pluginFailure(c, err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"version":  version,
		"settings": nonNilSlice(meta.Settings),
		"values":   settingsView(meta.Settings, plain, secrets),
	})
}

type pluginSettingsBody struct {
	Values map[string]any `json:"values"`
}

// putSettings validates and saves the submitted values. Keys that are not in
// the body keep their stored value. A null or empty secret clears it.
func (a *pluginDetailAPI) putSettings(c echo.Context) error {
	ctx := c.Request().Context()
	_, meta, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	var body pluginSettingsBody
	if err := c.Bind(&body); err != nil || body.Values == nil {
		return infraError(c, http.StatusBadRequest, "Invalid request: values is required")
	}

	raw, err := json.Marshal(body.Values)
	if err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request")
	}
	if len(raw) > pluginSettingsMaxJSON {
		return infraError(c, http.StatusRequestEntityTooLarge, "Settings exceed the size limit")
	}

	if err := plugin.CheckSettingValues(meta.Settings, body.Values); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}

	version, err := writePluginSettings(ctx, meta.ID, func(plain map[string]any, secrets map[string]string) {
		for key, value := range body.Values {
			if plugin.IsSecretSetting(meta.Settings, key) {
				text, _ := value.(string)
				if text == "" {
					delete(secrets, key)
				} else {
					secrets[key] = text
				}

				continue
			}

			if value == nil {
				delete(plain, key)
			} else {
				plain[key] = value
			}
		}
	})
	if err != nil {
		return pluginFailure(c, err)
	}

	keys := make([]string, 0, len(body.Values))
	for key := range body.Values {
		keys = append(keys, key)
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.settings.update", map[string]any{"plugin": meta.ID, "keys": keys, "version": version})

	return c.JSON(http.StatusOK, map[string]any{"version": version})
}

// resetSettings puts every non-secret setting back to its manifest default.
// Secrets are kept, because they have no default and clearing them would lose
// data the admin entered.
func (a *pluginDetailAPI) resetSettings(c echo.Context) error {
	ctx := c.Request().Context()
	_, meta, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	version, err := writePluginSettings(ctx, meta.ID, func(plain map[string]any, _ map[string]string) {
		for _, spec := range meta.Settings {
			if spec.Type == plugin.SettingSecret {
				continue
			}
			if spec.Default == nil {
				delete(plain, spec.Key)
			} else {
				plain[spec.Key] = spec.Default
			}
		}
	})
	if err != nil {
		return pluginFailure(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.settings.reset", map[string]any{"plugin": meta.ID, "version": version})

	return c.JSON(http.StatusOK, map[string]any{"version": version})
}

// writePluginSettings applies mutate to the stored settings in one transaction.
// It bumps the version, stores a snapshot of the new version and keeps only the
// newest pluginSnapshotKeep snapshots. The plugins row is locked first, so two
// writes to the same plugin run one after the other.
func writePluginSettings(ctx context.Context, id string, mutate func(plain map[string]any, secrets map[string]string)) (int, error) {
	var version int
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked []pluginRow
		if err := tx.Raw(`SELECT id FROM plugins WHERE id = ? FOR UPDATE`, id).Scan(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return errPluginNotFound
		}

		plain, secrets, current, err := readSettingsRow(tx, id)
		if err != nil {
			return err
		}
		mutate(plain, secrets)

		plainJSON, err := json.Marshal(plain)
		if err != nil {
			return err
		}
		var sealed any
		if len(secrets) > 0 {
			secretJSON, err := json.Marshal(secrets)
			if err != nil {
				return err
			}
			opened, err := settings.SealBytes(pluginSecretSealing+id, secretJSON)
			if err != nil {
				return err
			}
			sealed = opened
		}

		version = current + 1
		if err := tx.Exec(`
			INSERT INTO plugin_settings (plugin_id, "values", secrets_enc, version, updated_at)
			VALUES (?, CAST(? AS jsonb), ?, ?, now())
			ON CONFLICT (plugin_id) DO UPDATE SET
				"values" = EXCLUDED."values",
				secrets_enc = EXCLUDED.secrets_enc,
				version = EXCLUDED.version,
				updated_at = EXCLUDED.updated_at`,
			id, string(plainJSON), sealed, version).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO plugin_setting_snapshots (plugin_id, version, "values", secrets_enc)
			VALUES (?, ?, CAST(? AS jsonb), ?)`,
			id, version, string(plainJSON), sealed).Error; err != nil {
			return err
		}

		return tx.Exec(`DELETE FROM plugin_setting_snapshots WHERE plugin_id = ? AND version <= ?`,
			id, version-pluginSnapshotKeep).Error
	})

	return version, err
}

// test checks what can be checked without running plugin code: the manifest,
// the stored settings against their declarations, and whether the plugin is
// running. A plugin that is not running is reported, not treated as an error.
func (a *pluginDetailAPI) test(c echo.Context) error {
	id := c.Param("id")
	_, meta, err := findPlugin(id)
	if err != nil {
		return pluginFailure(c, err)
	}

	plain, secrets, _, err := readSettingsRow(data.DB, meta.ID)
	if err != nil {
		return pluginFailure(c, err)
	}
	stored := make(map[string]any, len(plain)+len(secrets))
	for key, value := range plain {
		stored[key] = value
	}
	for key, value := range secrets {
		stored[key] = value
	}
	settingsOK := plugin.CheckSettingValues(meta.Settings, stored) == nil

	runtimeDetail := "not running"
	if a.loaded(meta.ID) {
		runtimeDetail = "running"
	}

	return c.JSON(http.StatusOK, map[string]any{
		"ok": settingsOK,
		"checks": []map[string]any{
			{"name": "manifest", "ok": true},
			{"name": "settings", "ok": settingsOK},
			{"name": "runtime", "ok": a.loaded(meta.ID), "detail": runtimeDetail},
		},
	})
}

// action runs a declared action. A plugin that is not running is answered with
// dispatched false, so a request never reports work the plugin did not do.
func (a *pluginDetailAPI) action(c echo.Context) error {
	ctx := c.Request().Context()
	_, meta, err := findPlugin(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	name := c.Param("action")
	declared := false
	for _, action := range meta.Actions {
		if action.ID == name {
			declared = true

			break
		}
	}
	if !declared {
		return infraError(c, http.StatusNotFound, "Action not declared by the plugin")
	}

	if !a.loaded(meta.ID) {
		return c.JSON(http.StatusOK, map[string]any{"dispatched": false, "reason": "plugin_not_running"})
	}

	if err := a.runtime.Invoke(ctx, meta.ID, plugin.Hook("action:"+name), map[string]interface{}{"action": name}); err != nil {
		log.Printf("plugin %s: action %s failed: %v", meta.ID, name, err)

		return infraError(c, http.StatusBadGateway, "Plugin action failed")
	}

	return c.JSON(http.StatusOK, map[string]any{"dispatched": true})
}

type pluginLogView struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Level     int       `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id"`
}

// logs returns the newest system log rows of the plugin, at most
// pluginLogLimit, as a JSON array.
func (a *pluginDetailAPI) logs(c echo.Context) error {
	row, err := findPluginRow(c.Param("id"))
	if err != nil {
		return pluginFailure(c, err)
	}

	rows := make([]pluginLogView, 0)
	err = data.DB.Table("system_logs").
		Select("id, at, level, source, message, COALESCE(request_id, '') AS request_id").
		Where("plugin_id = ?", row.ID).
		Order("id DESC").
		Limit(pluginLogLimit).
		Find(&rows).Error
	if err != nil {
		return pluginFailure(c, err)
	}

	return c.JSON(http.StatusOK, rows)
}

type extensionView struct {
	Point     string   `json:"point"`
	Shipped   bool     `json:"shipped"`
	Providers []string `json:"providers"`
	Active    []string `json:"active"`
	// Resolved is the provider that serves the point now, or null when none can.
	Resolved *string `json:"resolved"`
}

// extensionProviders lists the providers a point can use: the builtin one for
// shipped points, then every provider registered by a loaded plugin.
func extensionProviders(point string) []string {
	providers := []string{}
	if ext.Shipped(point) {
		providers = append(providers, extensionBuiltinID)
	}
	for _, provider := range ext.Resolve(point) {
		if provider.ID != extensionBuiltinID {
			providers = append(providers, provider.ID)
		}
	}

	return providers
}

// extensionActive returns the active provider ids of point in position order.
func extensionActive(point string) ([]string, error) {
	var ids []string
	err := data.DB.Table("extension_bindings").
		Where("point = ?", point).
		Order("position ASC, provider_id ASC").
		Pluck("provider_id", &ids).Error
	if err != nil {
		return nil, err
	}

	return nonNilSlice(ids), nil
}

// resolveExtension returns the first active provider that is available. When
// none is, a shipped point falls back to builtin, and a declared-only point has
// no provider at all.
func resolveExtension(point string, active, providers []string) *string {
	available := make(map[string]bool, len(providers))
	for _, provider := range providers {
		available[provider] = true
	}
	for _, id := range active {
		if available[id] {
			return &id
		}
	}
	if ext.Shipped(point) {
		builtin := extensionBuiltinID

		return &builtin
	}

	return nil
}

func buildExtensionView(point string) (extensionView, error) {
	active, err := extensionActive(point)
	if err != nil {
		return extensionView{}, err
	}
	providers := extensionProviders(point)

	return extensionView{
		Point:     point,
		Shipped:   ext.Shipped(point),
		Providers: providers,
		Active:    active,
		Resolved:  resolveExtension(point, active, providers),
	}, nil
}

func (a *pluginDetailAPI) listExtensions(c echo.Context) error {
	points := ext.Points()
	views := make([]extensionView, 0, len(points))
	for _, point := range points {
		view, err := buildExtensionView(point)
		if err != nil {
			return pluginFailure(c, err)
		}
		views = append(views, view)
	}

	return c.JSON(http.StatusOK, views)
}

type extensionBody struct {
	Active []string `json:"active"`
}

// putExtension stores the ordered list of active providers of a point. Every id
// must be a provider the point knows about right now, so a stale or misspelled
// id is rejected instead of being stored.
func (a *pluginDetailAPI) putExtension(c echo.Context) error {
	ctx := c.Request().Context()
	point := c.Param("point")
	if !ext.Known(point) {
		return infraError(c, http.StatusNotFound, "Extension point not found")
	}
	if !ext.Shipped(point) {
		return infraError(c, http.StatusBadRequest, "Extension point has no implementation yet")
	}

	var body extensionBody
	if err := c.Bind(&body); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request")
	}
	if len(body.Active) > extensionMaxProviders {
		return infraError(c, http.StatusBadRequest, fmt.Sprintf("At most %d providers can be active", extensionMaxProviders))
	}

	providers := extensionProviders(point)
	known := make(map[string]bool, len(providers))
	for _, provider := range providers {
		known[provider] = true
	}
	seen := make(map[string]bool, len(body.Active))
	for _, id := range body.Active {
		if len(id) > extensionIDMaxLen || !known[id] {
			return infraError(c, http.StatusBadRequest, "Provider is not registered for this extension point")
		}
		if seen[id] {
			return infraError(c, http.StatusBadRequest, "Provider is listed twice")
		}
		seen[id] = true
	}

	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM extension_bindings WHERE point = ?`, point).Error; err != nil {
			return err
		}
		for position, id := range body.Active {
			if err := tx.Exec(`INSERT INTO extension_bindings (point, provider_id, position) VALUES (?, ?, ?)`,
				point, id, position).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return pluginFailure(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "extension.bind", map[string]any{"point": point, "count": len(body.Active)})

	view, err := buildExtensionView(point)
	if err != nil {
		return pluginFailure(c, err)
	}

	return c.JSON(http.StatusOK, view)
}
