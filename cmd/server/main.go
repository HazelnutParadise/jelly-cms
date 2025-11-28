package main

import (
	"fmt"
	"log"
	"os"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/server"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	// Initialize Echo
	e := echo.New()
	e.JSONSerializer = &JSONSerializer{} // Use goccy/go-json

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Database Connection (Basic check, real app should load from config)
	dbHost := os.Getenv("DB_HOST")
	if dbHost != "" {
		err := data.Connect(
			os.Getenv("DB_HOST"),
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_NAME"),
			os.Getenv("DB_PORT"),
		)
		if err != nil {
			log.Printf("Database connection failed (might be first run): %v", err)
		} else {
			// Auto Migrate
			if err := data.Migrate(); err != nil {
				log.Fatalf("Migration failed: %v", err)
			}
		}
	}

	// Theme Manager
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		log.Printf("Failed to activate default theme: %v", err)
	}

	// Initialize i18n
	i18n.Init()

	// Routes
	server.RegisterRoutes(e, tm)

	e.Logger.Fatal(e.Start(":8080"))
}

// JSONSerializer implements Echo's JSONSerializer interface using goccy/go-json
type JSONSerializer struct{}

func (j *JSONSerializer) Serialize(c echo.Context, i interface{}, indent string) error {
	enc := json.NewEncoder(c.Response())
	if indent != "" {
		enc.SetIndent("", indent)
	}
	return enc.Encode(i)
}

func (j *JSONSerializer) Deserialize(c echo.Context, i interface{}) error {
	err := json.NewDecoder(c.Request().Body).Decode(i)
	if ute, ok := err.(*json.UnmarshalTypeError); ok {
		return echo.NewHTTPError(400, fmt.Sprintf("Unmarshal type error: expected=%v, got=%v, field=%v, offset=%v", ute.Type, ute.Value, ute.Field, ute.Offset)).SetInternal(err)
	} else if se, ok := err.(*json.SyntaxError); ok {
		return echo.NewHTTPError(400, fmt.Sprintf("Syntax error: offset=%v, error=%v", se.Offset, se.Error())).SetInternal(err)
	}
	return err
}
