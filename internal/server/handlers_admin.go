package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"gorm.io/gorm"
)

// uploadBodyLimit is the request size cap for routes that accept files. Every
// other route is limited to the global 2 MiB in RegisterRoutes.
const uploadBodyLimit = "64M"

func RegisterAdminRoutes(e *echo.Echo, tm *theme.Manager) {
	registerAdminRoutes(e, tm, nil)
}

func registerAdminRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	// Content and media are available to known content roles. All other admin
	// endpoints are registered on the admin-only group below.
	content := e.Group("/api/admin", auth.RequireRole(contentRoles...))
	postReads := e.Group("/api/admin")
	registerPostRoutes(content, postReads)
	api := e.Group("/api/admin", auth.RequireRole(adminRoles...))
	registerEditorRoutes(e, content, api)

	// Media
	content.POST("/media", handleLegacyMediaUpload, middleware.BodyLimit(uploadBodyLimit))
	content.DELETE("/media/:filename", handleLegacyMediaDelete)
	content.PUT("/media/:filename", handleLegacyMediaReplace, middleware.BodyLimit(uploadBodyLimit))

	content.POST("/media/rename", renameLegacyMedia)
	content.GET("/media", listLegacyMedia)

	// Themes
	api.GET("/themes", func(c echo.Context) error { return listThemes(c, tm) })

	api.POST("/themes/activate", func(c echo.Context) error { return activateTheme(c, tm) })

	// Get theme config
	api.GET("/themes/:name/config", func(c echo.Context) error { return getThemeConfig(c, tm) })

	// Get theme settings
	api.GET("/themes/:name/settings", func(c echo.Context) error { return getThemeSettings(c, tm) })

	// Save theme settings
	api.POST("/themes/:name/settings", func(c echo.Context) error { return saveThemeSettings(c, tm) })

	// Plugins
	api.GET("/plugins", listPlugins)

	// Upload plugin. The archive is validated and extracted by installPluginFile,
	// which also records the plugins row (disabled until an admin enables it).
	// The field is "file"; "plugin" is still read for the existing admin screen.
	api.POST("/plugins/upload", func(c echo.Context) error {
		file, err := c.FormFile("file")
		if err != nil {
			file, err = c.FormFile("plugin")
		}
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		}

		// Check file extension
		if !strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Only ZIP files are supported"})
		}

		// Open uploaded file
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to open uploaded file"})
		}
		defer src.Close()

		// Copy the upload to a temporary file with a generated name.
		tmp, err := os.CreateTemp("", "jelly-plugin-*.zip")
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create temporary file"})
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err = io.Copy(tmp, src); err != nil {
			tmp.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save uploaded file"})
		}
		if err := tmp.Close(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save uploaded file"})
		}
		digest, err := hashPluginArchive(tmpPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to inspect uploaded file"})
		}
		meta, err := plugin.ValidateArchive(tmpPath)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		permissions := plugin.ManifestPermissionNames(meta)
		permissionsHash := plugin.PermissionNamesHash(permissions)
		if c.QueryParam("dry") == "1" {
			return c.JSON(http.StatusOK, map[string]any{"id": meta.ID, "version": meta.Version, "permissions": permissions, "permissions_hash": permissionsHash, "sha256": digest})
		}
		if len(permissions) > 0 {
			if c.FormValue("accept_permissions") != permissionsHash || c.FormValue("sha256") != digest {
				return c.JSON(http.StatusConflict, map[string]string{"error": "Preview this exact archive and accept its permissions before installing"})
			}
		}

		res, err := installPluginFile(c.Request().Context(), pluginRuntime, tmpPath, pluginFileInstall{Source: storeSourceUpload, Signature: storeSignatureNone, Want: plugin.ArchiveExpectation{ID: meta.ID, Version: meta.Version, PermissionsHash: permissionsHash}})
		if err != nil {
			switch {
			case errors.Is(err, errPluginRunning):
				return c.JSON(http.StatusConflict, map[string]string{"error": "Plugin is running; disable it before uploading a new version"})
			case errors.Is(err, errPluginRecoveryPending):
				return c.JSON(http.StatusConflict, map[string]string{"error": "Plugin recovery is pending"})
			case plugin.IsArchiveError(err):
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Plugin installation failed"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Plugin uploaded and extracted successfully", "plugin_id": res.ID})
	}, middleware.BodyLimit(uploadBodyLimit))

	// Reload plugin
	api.POST("/plugins/:id/reload", func(c echo.Context) error { return reloadPlugin(c, pluginRuntime) })

	// Settings: GET masks, PUT and POST are the same audited, registry-checked write.
	api.GET("/settings", handleSettingsList)
	api.POST("/settings", handleSettingsWrite)
	api.PUT("/settings", handleSettingsWrite)

	// Payment Gateway Settings
	api.GET("/payment/gateways", func(c echo.Context) error {
		// Get payment service from global or context
		// For now, we'll create a temporary service to get configs
		service := payment.NewService(data.DB)
		configs := service.GetPaymentConfigs()
		return c.JSON(http.StatusOK, configs)
	})

	api.POST("/payment/gateways/ecpay", func(c echo.Context) error {
		if data.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		var req struct {
			Enabled    bool   `json:"enabled"`
			MerchantID string `json:"merchant_id"`
			HashKey    string `json:"hash_key"`
			HashIV     string `json:"hash_iv"`
			TestMode   bool   `json:"test_mode"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		service := payment.NewService(data.DB)
		config := &payment.ECPayConfig{
			MerchantID: req.MerchantID,
			HashKey:    req.HashKey,
			HashIV:     req.HashIV,
			IsTestMode: req.TestMode,
		}

		if err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
			if err := lockShopSettings(tx); err != nil {
				return err
			}
			return service.SaveECPayConfig(config, req.Enabled)
		}); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid gateway configuration"})
		}

		// Payment service will reload from DB on next use
		// The service is initialized in main.go and will reload configs when needed

		return c.JSON(http.StatusOK, map[string]string{"message": "ECPay configuration saved"})
	})

	api.POST("/payment/gateways/newebpay", func(c echo.Context) error {
		if data.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		var req struct {
			Enabled    bool   `json:"enabled"`
			MerchantID string `json:"merchant_id"`
			HashKey    string `json:"hash_key"`
			HashIV     string `json:"hash_iv"`
			TestMode   bool   `json:"test_mode"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		service := payment.NewService(data.DB)
		config := &payment.NewebPayConfig{
			MerchantID: req.MerchantID,
			HashKey:    req.HashKey,
			HashIV:     req.HashIV,
			IsTestMode: req.TestMode,
		}

		if err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
			if err := lockShopSettings(tx); err != nil {
				return err
			}
			return service.SaveNewebPayConfig(config, req.Enabled)
		}); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid gateway configuration"})
		}

		// Payment service will reload from DB on next use
		// The service is initialized in main.go and will reload configs when needed

		return c.JSON(http.StatusOK, map[string]string{"message": "NewebPay configuration saved"})
	})

	// OAuth Provider Settings
	api.GET("/oauth/providers", func(c echo.Context) error {
		if data.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}

		keys := []string{
			"oauth_google_enabled", "oauth_google_client_id", "oauth_google_client_secret",
			"oauth_github_enabled", "oauth_github_client_id", "oauth_github_client_secret",
		}
		var options []core.Option
		if err := data.DB.WithContext(c.Request().Context()).Where("key IN ?", keys).Find(&options).Error; err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		values := make(map[string]string, len(options))
		for _, option := range options {
			values[option.Key] = option.Value
		}

		googleConfig := make(map[string]string)
		for _, key := range []string{"oauth_google_client_id", "oauth_google_client_secret"} {
			if value, ok := values[key]; ok {
				googleConfig[key] = value
			}
		}
		githubConfig := make(map[string]string)
		for _, key := range []string{"oauth_github_client_id", "oauth_github_client_secret"} {
			if value, ok := values[key]; ok {
				githubConfig[key] = value
			}
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"google": map[string]interface{}{
				"enabled": values["oauth_google_enabled"] == "true",
				"config":  googleConfig,
			},
			"github": map[string]interface{}{
				"enabled": values["oauth_github_enabled"] == "true",
				"config":  githubConfig,
			},
		})
	})

	api.POST("/oauth/providers/google", func(c echo.Context) error {
		var req struct {
			Enabled      bool   `json:"enabled"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		settings := map[string]string{
			"oauth_google_enabled":       fmt.Sprintf("%v", req.Enabled),
			"oauth_google_client_id":     req.ClientID,
			"oauth_google_client_secret": req.ClientSecret,
		}

		for k, v := range settings {
			var opt core.Option
			result := data.DB.Where("key = ?", k).First(&opt)
			if result.Error != nil {
				opt = core.Option{Key: k, Value: v}
			} else {
				opt.Value = v
			}
			if err := data.DB.Save(&opt).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}

		// Reload OAuth providers
		auth.ReloadOAuthProviders()

		return c.JSON(http.StatusOK, map[string]string{"message": "Google OAuth configuration saved"})
	})

	api.POST("/oauth/providers/github", func(c echo.Context) error {
		var req struct {
			Enabled      bool   `json:"enabled"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		settings := map[string]string{
			"oauth_github_enabled":       fmt.Sprintf("%v", req.Enabled),
			"oauth_github_client_id":     req.ClientID,
			"oauth_github_client_secret": req.ClientSecret,
		}

		for k, v := range settings {
			var opt core.Option
			result := data.DB.Where("key = ?", k).First(&opt)
			if result.Error != nil {
				opt = core.Option{Key: k, Value: v}
			} else {
				opt.Value = v
			}
			if err := data.DB.Save(&opt).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}

		// Reload OAuth providers
		auth.ReloadOAuthProviders()

		return c.JSON(http.StatusOK, map[string]string{"message": "GitHub OAuth configuration saved"})
	})
}
