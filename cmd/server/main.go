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
	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/HazelnutParadise/jelly-cms/internal/server"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	plugin.RunWorkerIfRequested()

	// The updater runs "jelly-cms healthcheck" on a freshly installed binary
	// before it trusts it. The check touches nothing else and exits at once.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := update.WriteHealthcheck(os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}

	// A binary that two starts in a row failed to confirm is replaced by the
	// previous one before anything else runs (ticket 08c).
	relaunch, err := update.GuardStartup()
	if err != nil {
		log.Printf("update: startup guard: %v", err)
	}
	if relaunch {
		if err := update.Relaunch(); err != nil {
			log.Printf("update: relaunch restored executable: %v", err)
		} else {
			return
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Let run's deferred cleanup finish before deciding the process exit code.
	runErr := run(ctx)
	if runErr != nil {
		log.Printf("HTTP server stopped: %v", runErr)
	}
	// An apply or rollback asked for a restart. The new process starts after the
	// old server has stopped, so it can bind the same address. This happens even
	// when the shutdown reported an error.
	if update.RestartPending() {
		if err := update.Relaunch(); err != nil {
			log.Printf("update: relaunch after restart request: %v", err)
			os.Exit(1)
		}
		return
	}
	if runErr != nil {
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	// An update restart request stops the server the same way a signal does.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		select {
		case <-update.RestartRequested():
			cancel()
		case <-ctx.Done():
		}
	}()

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
			if err := pluginRuntime.LoadWhere(context.Background(), "data/plugins", server.PluginEnabledFilter()); err != nil {
				log.Printf("Failed to load plugins: %v", err)
			}
		}
	}

	// Routes
	server.RegisterRoutes(e, tm, pluginRuntime)

	go runTrashPurge(ctx)
	go runLogsPrune(ctx)
	go runRedirectHits(ctx)
	go runBackupSchedule(ctx)
	go runOrderExpiry(ctx)
	go server.RerenderStalePosts(ctx)

	// The server is about to serve; an applied version has now started cleanly.
	if err := update.ConfirmStarted(); err != nil {
		log.Printf("update: confirm start: %v", err)
	}
	return runHTTPServer(ctx, e, ":8080")
}

// runBackupSchedule checks every 30 seconds whether the daily scheduled backup
// is due. Two checks fall in every minute, so the due minute is never missed,
// and backup.RunScheduledIfDue starts at most one backup per day.
func runBackupSchedule(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if data.DB == nil {
				continue
			}
			if err := backup.RunScheduledIfDue(ctx, now); err != nil && ctx.Err() == nil {
				log.Printf("Scheduled backup: %v", err)
			}
		}
	}
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

// runLogsPrune removes system log rows past their retention window at start
// and then every 24 hours until ctx is done. It does nothing while the site is
// not installed (data.DB nil).
func runLogsPrune(ctx context.Context) {
	prune := func() {
		if data.DB == nil {
			return
		}
		removed, err := server.PruneSystemLogs(ctx, data.DB, time.Now().UTC())
		if err != nil && ctx.Err() == nil {
			log.Printf("Log prune: %v", err)
		}
		if removed > 0 {
			log.Printf("Log prune: removed %d expired rows", removed)
		}
	}
	prune()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// runRedirectHits writes redirect hit counts to the database every 30 seconds
// until ctx is done. Counts not yet written when the process exits are lost.
func runRedirectHits(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if data.DB == nil {
				continue
			}
			if err := redirects.FlushHits(); err != nil {
				log.Printf("Redirect hit flush: %v", err)
			}
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
