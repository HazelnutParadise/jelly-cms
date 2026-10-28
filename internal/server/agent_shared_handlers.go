package server

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func listLegacyMedia(c echo.Context) error {
	files, err := os.ReadDir("data/uploads")
	if err != nil {
		return c.JSON(200, []string{})
	}
	var out []string
	for _, f := range files {
		if !f.IsDir() {
			out = append(out, "/uploads/"+f.Name())
		}
	}
	return c.JSON(200, out)
}
func renameLegacyMedia(c echo.Context) error {
	var req struct {
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request"})
	}
	if filepath.Base(req.OldName) != req.OldName || filepath.Base(req.NewName) != req.NewName {
		return c.JSON(400, map[string]string{"error": "Invalid filename"})
	}
	return moveLegacyMedia(c, req.OldName, req.NewName)
}
func listThemes(c echo.Context, tm *theme.Manager) error {
	entries, err := os.ReadDir("web/themes")
	if err != nil {
		return err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return c.JSON(200, out)
}
func activateTheme(c echo.Context, tm *theme.Manager) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return err
	}
	if data.DB == nil {
		return c.JSON(500, map[string]string{"error": "database is unavailable"})
	}
	db := data.DB.WithContext(c.Request().Context())
	if err := tm.ActivateAndPersist(req.Name, db); err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}
	return c.JSON(200, map[string]string{"message": "Theme activated"})
}
func getThemeConfig(c echo.Context, tm *theme.Manager) error {
	cfg, err := tm.Load(c.Param("name"))
	if err != nil {
		return c.JSON(404, map[string]string{"error": "Theme not found"})
	}
	return c.JSON(200, cfg)
}
func getThemeSettings(c echo.Context, tm *theme.Manager) error {
	v, err := tm.GetSettings(c.Param("name"), data.DB)
	if err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}
	return c.JSON(200, v)
}
func saveThemeSettings(c echo.Context, tm *theme.Manager) error {
	var req struct {
		Colors any `json:"colors"`
		Layout any `json:"layout"`
		Custom any `json:"custom"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request"})
	}
	conv := func(v any) (json.RawMessage, error) {
		if v == nil {
			return nil, nil
		}
		if s, ok := v.(string); ok {
			return json.RawMessage(s), nil
		}
		b, e := json.Marshal(v)
		return b, e
	}
	colors, e := conv(req.Colors)
	if e != nil {
		return c.JSON(400, map[string]string{"error": "Invalid colors format"})
	}
	layout, e := conv(req.Layout)
	if e != nil {
		return c.JSON(400, map[string]string{"error": "Invalid layout format"})
	}
	custom, e := conv(req.Custom)
	if e != nil {
		return c.JSON(400, map[string]string{"error": "Invalid custom format"})
	}
	v := &core.ThemeSettings{ThemeName: c.Param("name"), Colors: colors, Layout: layout, Custom: custom}
	if err := tm.SaveSettings(v, data.DB); err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}
	active := tm.GetActive()
	if err := tm.Activate(active); err != nil {
		return c.JSON(500, map[string]string{"error": "settings were saved but theme reload failed"})
	}
	return c.JSON(200, map[string]string{"message": "Settings saved"})
}
func listPlugins(c echo.Context) error {
	entries, err := os.ReadDir("data/plugins")
	if err != nil {
		return c.JSON(200, []any{})
	}
	var out []map[string]any
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join("data", "plugins", e.Name(), "plugin.json")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta plugin.PluginMetadata
		if json.Unmarshal(b, &meta) == nil {
			out = append(out, map[string]any{"id": meta.ID, "name": meta.Name, "version": meta.Version, "description": meta.Description, "author": meta.Author})
		}
	}
	return c.JSON(200, out)
}
func reloadPlugin(c echo.Context, rt *plugin.Runtime) error {
	if rt == nil {
		return c.JSON(503, map[string]string{"error": "Plugin runtime is unavailable"})
	}
	meta, err := rt.GetPlugin(c.Param("id"))
	if err != nil {
		return c.JSON(404, map[string]string{"error": "Plugin not found"})
	}
	dir := filepath.Join("data", "plugins", meta.ID)
	if err := rt.ReloadForID(c.Request().Context(), meta.ID, dir); err != nil {
		return c.JSON(500, map[string]string{"error": "Plugin reload failed"})
	}
	return c.JSON(200, map[string]string{"message": "Plugin reloaded"})
}
