package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

func currentUser(c echo.Context) (*core.User, error) {
	user, ok := c.Get("user").(*core.User)
	if !ok || user == nil {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	return user, nil
}
