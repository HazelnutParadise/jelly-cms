package main

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func requestLogger() echo.MiddlewareFunc {
	config := middleware.DefaultLoggerConfig
	config.Format = strings.ReplaceAll(config.Format, "${uri}", "${path}")
	return middleware.LoggerWithConfig(config)
}
