package server

import "github.com/labstack/echo/v4"

// renderSystemPage keeps each system tool in the shared admin shell. The
// bundled system-admin.js supplies the page's forms and readback from its API.
func renderSystemPage(page, active, titleKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return renderAdminPage(c, AdminPage{
			File: "system.html", Active: active,
			Data: map[string]interface{}{
				"SystemPage": page, "PageTitle": titleKey, "PluginID": c.Param("id"),
			},
		})
	}
}
