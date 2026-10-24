package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/server"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	plugin.RunWorkerIfRequested()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Let run's deferred cleanup finish before deciding the process exit code.
	if err := run(ctx); err != nil {
		log.Printf("HTTP server stopped: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Initialize Echo
	e := echo.New()
	e.JSONSerializer = &JSONSerializer{} // Use goccy/go-json

	e.Use(requestLogger())
	e.Use(middleware.Recover())

	// Recover any interrupted installation, then load the site configuration
	// and connect to its database. The site stays closed to installation when
	// this fails; the HTTP layer answers 503 while data.DB is nil.
	if err := initializeSite(); err != nil {
		log.Printf("Site initialization failed: %v", err)
	}

	// Theme Manager
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		log.Printf("Failed to activate default theme: %v", err)
	}

	// Initialize i18n
	i18n.Init()

	// Initialize auth
	if err := auth.Init(); err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}

	// Initialize payment service
	if config.GlobalConfig != nil && data.DB != nil {
		server.InitPaymentService(data.DB)
	}

	// Initialize plugin runtime
	pluginRuntime, err := plugin.NewRuntime()
	if err != nil {
		log.Printf("Failed to initialize plugin runtime: %v", err)
	} else {
		defer pluginRuntime.Close()

		// Load all plugins if database is connected
		if config.GlobalConfig != nil && data.DB != nil {
			os.MkdirAll("data/plugins", 0755)
			if err := pluginRuntime.LoadAll(context.Background(), "data/plugins"); err != nil {
				log.Printf("Failed to load plugins: %v", err)
			}
		}
	}

	// Routes
	server.RegisterRoutes(e, tm, pluginRuntime)

	go runTrashPurge(ctx)
	go runOrderExpiry(ctx)
	go server.RerenderStalePosts(ctx)

	return runHTTPServer(ctx, e, ":8080")
}

// runOrderExpiry releases stock and coupon holds for orders whose payment
// window elapsed. Each sweep is bounded by the process context and timeout.
func runOrderExpiry(ctx context.Context) {
	sweep := func() {
		if data.DB == nil {
			return
		}
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		count, err := core.ExpireUnpaidOrders(data.DB.WithContext(sweepCtx), time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Order expiry sweep failed: %v", err)
			}
			return
		}
		if count > 0 {
			log.Printf("Order expiry sweep: expired %d pending orders", count)
		}
	}
	sweep()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// runTrashPurge removes expired recycle-bin rows at start and then every 24 hours
// until ctx is done. It does nothing while the site is not installed (data.DB nil).
func runTrashPurge(ctx context.Context) {
	purge := func() {
		if data.DB == nil {
			return
		}
		removed, err := server.PurgeExpiredTrash(data.DB, time.Now())
		if err != nil {
			log.Printf("Trash purge: %v", err)
		}
		if removed > 0 {
			log.Printf("Trash purge: removed %d expired rows", removed)
		}
	}
	purge()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
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
