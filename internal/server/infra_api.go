package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/cache"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
)

// Infrastructure API (ticket 08, 「快取與佇列」): cache and queue settings,
// connection tests, flushes, the queue switch, pause and resume, and the jobs
// list. Environment variables win over the stored settings.

const (
	envCacheURL     = "JELLY_CACHE_URL"
	envQueueURL     = "JELLY_QUEUE_URL"
	envQueueWorkers = "JELLY_QUEUE_WORKERS"

	keyCacheDriver  = "cache_driver"
	keyCacheURL     = "cache_url_enc"
	keyCacheTTL     = "cache_ttl_page"
	keyCacheObject  = "cache_object_enabled"
	keyQueueDriver  = "queue_driver"
	keyQueueURL     = "queue_url_enc"
	keyQueueWorkers = "queue_workers"
	keyQueuePaused  = "queue_paused"

	// Cache namespaces that the site's own caches write under. A flush scope
	// clears these; plugin namespaces are cleared by the plugin.
	cacheNamespacePage   = "page"
	cacheNamespaceObject = "object"

	maxQueueWorkers = 64
	maxCacheTTL     = 86400
)

var (
	cacheDrivers = map[string]bool{cache.DriverMemory: true, cache.DriverRedis: true}
	queueDrivers = map[string]bool{
		queue.DriverPostgres: true, queue.DriverMemory: true,
		queue.DriverRedis: true, queue.DriverNATS: true,
	}
	// connSchemes lists the drivers that take a connection URL and the URL
	// schemes they accept. Drivers missing here take no URL.
	connSchemes = map[string][]string{
		cache.DriverRedis: {"redis", "rediss"},
		queue.DriverNATS:  {"nats", "tls"},
	}
	cacheScopes = map[string][]string{
		"page":   {cacheNamespacePage},
		"object": {cacheNamespaceObject},
		"all":    {cacheNamespacePage, cacheNamespaceObject},
	}
	jobStates        = map[string]bool{"queued": true, "running": true, "done": true, "failed": true}
	queueNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	urlInText        = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S*`)
)

// connConfig is the driver and connection URL the server uses, with where the
// URL came from. Source is one of env, settings, request or none.
type connConfig struct {
	Driver string
	URL    string
	Source string
}

// newConnConfig drops the URL for drivers that take none, so configs that
// connect the same way always compare equal.
func newConnConfig(driver, rawURL, source string) connConfig {
	if _, ok := connSchemes[driver]; !ok {
		return connConfig{Driver: driver, Source: "none"}
	}
	return connConfig{Driver: driver, URL: rawURL, Source: source}
}

func (c connConfig) signature() string { return c.Driver + "\x00" + c.URL }

// resolveConn reads the driver and URL from settings. An environment URL
// takes precedence over the stored one.
func resolveConn(ctx context.Context, driverKey, urlKey, envKey, fallback string) connConfig {
	driver := normalizeDriver(settings.Get(ctx, driverKey))
	if driver == "" {
		driver = fallback
	}
	if env := strings.TrimSpace(os.Getenv(envKey)); env != "" {
		return newConnConfig(driver, env, "env")
	}
	if stored := settings.Get(ctx, urlKey); stored != "" {
		return newConnConfig(driver, stored, "settings")
	}
	return newConnConfig(driver, "", "none")
}

func cacheConfig(ctx context.Context) connConfig {
	return resolveConn(ctx, keyCacheDriver, keyCacheURL, envCacheURL, cache.DriverMemory)
}

func queueConfig(ctx context.Context) connConfig {
	return resolveConn(ctx, keyQueueDriver, keyQueueURL, envQueueURL, queue.DriverPostgres)
}

func queueDriverAllowed(driver string) bool {
	if !queueDrivers[driver] {
		return false
	}
	return driver != queue.DriverMemory || os.Getenv("JELLY_TEST_POSTGRES") == "1"
}

// queueWorkers returns the worker count: JELLY_QUEUE_WORKERS, then the setting.
func queueWorkers(ctx context.Context) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envQueueWorkers))); err == nil && n >= 1 && n <= maxQueueWorkers {
		return n
	}
	n := settingInt(ctx, keyQueueWorkers)
	return min(max(n, 1), maxQueueWorkers)
}

// settingInt reads an integer option, falling back to the registered default.
func settingInt(ctx context.Context, key string) int {
	if n, err := strconv.Atoi(settings.Get(ctx, key)); err == nil {
		return n
	}
	spec, _ := settings.Lookup(key)
	n, _ := strconv.Atoi(spec.Default)
	return n
}

func normalizeDriver(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// infraRuntime holds the open cache store and queue. Each is reopened when its
// configuration changes. RegisterRoutes creates one per server, so a switch
// made through the API is what every later request sees.
type infraRuntime struct {
	mu       sync.Mutex
	cache    cache.Store
	cacheSig string
	queue    queue.Queue
	queueSig string
}

func newInfraRuntime() *infraRuntime { return &infraRuntime{} }

// activeCache returns the store for the configured driver.
func (rt *infraRuntime) activeCache(ctx context.Context) (cache.Store, connConfig, error) {
	cfg := cacheConfig(ctx)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.cache != nil && rt.cacheSig == cfg.signature() {
		return rt.cache, cfg, nil
	}
	store, err := cache.Open(cache.Config{Driver: cfg.Driver, URL: cfg.URL})
	if err != nil {
		return nil, cfg, err
	}
	if rt.cache != nil {
		_ = rt.cache.Close()
	}
	rt.cache, rt.cacheSig = store, cfg.signature()
	return store, cfg, nil
}

// activeQueue returns the queue for the configured driver.
func (rt *infraRuntime) activeQueue(ctx context.Context) (queue.Queue, connConfig, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.activeQueueLocked(ctx)
}

// activeQueueLocked is activeQueue for a caller that already holds rt.mu.
func (rt *infraRuntime) activeQueueLocked(ctx context.Context) (queue.Queue, connConfig, error) {
	cfg := queueConfig(ctx)
	if systemQueueRecoveryPending(data.DB) {
		return nil, cfg, errors.New("queue transfer recovery is pending")
	}
	if !queueDriverAllowed(cfg.Driver) {
		return nil, cfg, errors.New("queue driver is not allowed in production")
	}
	if shared, actual, ok := systemQueueForDB(data.DB); ok {
		if actual.signature() != cfg.signature() {
			requestSystemQueueRecovery(data.DB)
			return nil, cfg, errors.New("queue configuration changed; runtime recovery is pending")
		}
		rt.queue, rt.queueSig = shared, actual.signature()
		return shared, actual, nil
	}
	if rt.queue != nil && rt.queueSig == cfg.signature() {
		return rt.queue, cfg, nil
	}
	q, err := queue.Open(queue.Config{Driver: cfg.Driver, URL: cfg.URL, Workers: queueWorkers(ctx)})
	if err != nil {
		return nil, cfg, err
	}
	if rt.queue != nil {
		_ = rt.queue.Close()
	}
	rt.queue, rt.queueSig = q, cfg.signature()
	return q, cfg, nil
}

// inspector returns the active queue and its admin surface.
func (rt *infraRuntime) inspector(ctx context.Context) (queue.Queue, queue.Inspector, error) {
	q, _, err := rt.activeQueue(ctx)
	if err != nil {
		return nil, nil, err
	}
	ins, ok := q.(queue.Inspector)
	if !ok {
		return nil, nil, fmt.Errorf("queue driver %T has no admin surface", q)
	}
	return q, ins, nil
}

// probeCache opens a store for cfg, pings it and closes it. The active store is untouched.
func probeCache(ctx context.Context, cfg connConfig) (time.Duration, error) {
	store, err := cache.Open(cache.Config{Driver: cfg.Driver, URL: cfg.URL})
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	return store.Ping(ctx)
}

// probeQueue opens a queue for cfg, pings it and closes it. The active queue is untouched.
func probeQueue(ctx context.Context, cfg connConfig, workers int) (time.Duration, error) {
	if !queueDriverAllowed(cfg.Driver) {
		return 0, errors.New("queue driver is not allowed in production")
	}
	q, err := queue.Open(queue.Config{Driver: cfg.Driver, URL: cfg.URL, Workers: workers})
	if err != nil {
		return 0, err
	}
	defer func() { _ = q.Close() }()
	return q.Ping(ctx)
}

// safeError returns err's text with the connection URL and its password
// removed. Driver errors can echo the URL they were given.
func safeError(err error, rawURL string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if rawURL != "" {
		msg = strings.ReplaceAll(msg, rawURL, "[url]")
	}
	if u, perr := url.Parse(rawURL); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "[redacted]")
		}
	}
	return urlInText.ReplaceAllString(msg, "[url]")
}

// validateConnURL checks the shape of a connection URL for a driver that
// takes one. Its messages never include the URL: url.Parse errors quote the
// whole input, so that error is replaced rather than returned.
func validateConnURL(driver, rawURL string) error {
	schemes, ok := connSchemes[driver]
	if !ok {
		return nil
	}
	if rawURL == "" {
		return errors.New("connection URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("connection URL is malformed")
	}
	if !slices.Contains(schemes, strings.ToLower(u.Scheme)) {
		return fmt.Errorf("connection URL must use the %s scheme", driver)
	}
	if u.Host == "" {
		return errors.New("connection URL has no host")
	}
	return nil
}

func infraActor(c echo.Context) uint {
	if user, ok := auth.GetUserFromContext(c); ok && user != nil {
		return user.ID
	}
	return 0
}

func infraError(c echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]string{"error": msg})
}

// settingsWriteError maps a settings.Set failure to a status code.
func settingsWriteError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, settings.ErrTooLarge):
		return infraError(c, http.StatusRequestEntityTooLarge, "Setting value is too large")
	case errors.Is(err, settings.ErrUnknownKey), errors.Is(err, settings.ErrInvalidValue):
		return infraError(c, http.StatusBadRequest, "Invalid setting value")
	}
	return infraError(c, http.StatusInternalServerError, "Settings save failed")
}

// GET /api/admin/infra

func (rt *infraRuntime) handleStatus(c echo.Context) error {
	ctx := c.Request().Context()
	return c.JSON(http.StatusOK, map[string]any{
		"cache": rt.cacheStatus(ctx),
		"queue": rt.queueStatus(ctx),
	})
}

func (rt *infraRuntime) cacheStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"connected":      false,
		"latency_ms":     int64(0),
		"stats":          map[string]any{},
		"ttl_page":       settingInt(ctx, keyCacheTTL),
		"object_enabled": settings.Get(ctx, keyCacheObject) == "true",
	}
	store, cfg, err := rt.activeCache(ctx)
	out["driver"], out["url_source"] = cfg.Driver, cfg.Source
	if err != nil {
		out["error"] = safeError(err, cfg.URL)
		return out
	}
	latency, err := store.Ping(ctx)
	if err != nil {
		out["error"] = safeError(err, cfg.URL)
	} else {
		out["connected"], out["latency_ms"] = true, latency.Milliseconds()
	}
	if st, err := store.Stats(ctx); err != nil {
		out["error"] = safeError(err, cfg.URL)
	} else {
		out["stats"] = map[string]any{"entries": st.Entries, "bytes": st.Bytes, "hits": st.Hits, "misses": st.Misses}
	}
	return out
}

func (rt *infraRuntime) queueStatus(ctx context.Context) map[string]any {
	out := map[string]any{
		"connected":  false,
		"latency_ms": int64(0),
		"workers":    queueWorkers(ctx),
		"paused":     settings.Get(ctx, keyQueuePaused) == "true",
		"queues":     []map[string]any{},
	}
	q, cfg, err := rt.activeQueue(ctx)
	out["driver"], out["url_source"] = cfg.Driver, cfg.Source
	if err != nil {
		out["error"] = safeError(err, cfg.URL)
		return out
	}
	latency, err := q.Ping(ctx)
	if err != nil {
		out["error"] = safeError(err, cfg.URL)
	} else {
		out["connected"], out["latency_ms"] = true, latency.Milliseconds()
	}
	stats, err := q.Stats(ctx)
	if err != nil {
		out["error"] = safeError(err, cfg.URL)
		return out
	}
	queues := make([]map[string]any, 0, len(stats))
	for _, s := range stats {
		queues = append(queues, map[string]any{
			"queue": s.Queue, "queued": s.Queued, "running": s.Running, "done": s.Done, "failed": s.Failed,
		})
	}
	out["queues"] = queues
	return out
}

// PUT /api/admin/infra/cache

type cacheUpdateRequest struct {
	Driver        *string `json:"driver"`
	URL           *string `json:"url"`
	TTLPage       *int    `json:"ttl_page"`
	ObjectEnabled *bool   `json:"object_enabled"`
}

// handleCachePut saves the cache settings. A driver that cannot connect is
// refused, so a broken configuration is never stored. The cache holds no
// durable data, so a driver change needs no migration.
func (rt *infraRuntime) handleCachePut(c echo.Context) error {
	var req cacheUpdateRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	current := cacheConfig(ctx)
	driver := current.Driver
	if req.Driver != nil {
		driver = normalizeDriver(*req.Driver)
	}
	if !cacheDrivers[driver] {
		return infraError(c, http.StatusBadRequest, "Unknown cache driver")
	}
	kv := map[string]string{keyCacheDriver: driver}
	if req.TTLPage != nil {
		if *req.TTLPage < 1 || *req.TTLPage > maxCacheTTL {
			return infraError(c, http.StatusBadRequest, "ttl_page must be 1 to 86400")
		}
		kv[keyCacheTTL] = strconv.Itoa(*req.TTLPage)
	}
	if req.ObjectEnabled != nil {
		kv[keyCacheObject] = strconv.FormatBool(*req.ObjectEnabled)
	}
	target := current.URL
	if req.URL != nil {
		if os.Getenv(envCacheURL) != "" {
			return infraError(c, http.StatusConflict, "JELLY_CACHE_URL is set; change the URL in the environment")
		}
		target = strings.TrimSpace(*req.URL)
		kv[keyCacheURL] = target
	}
	cfg := newConnConfig(driver, target, "settings")
	if err := validateConnURL(driver, cfg.URL); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	if _, err := probeCache(ctx, cfg); err != nil {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "Connection test failed: " + safeError(err, cfg.URL)})
	}
	if err := settings.Set(ctx, infraActor(c), kv); err != nil {
		return settingsWriteError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "Cache settings saved", "driver": driver})
}

// connTestRequest is the body of the two connection-test endpoints. An empty
// URL means the saved URL, when the driver is the one in use.
type connTestRequest struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// POST /api/admin/infra/cache/test

func (rt *infraRuntime) handleCacheTest(c echo.Context) error {
	var req connTestRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	current := cacheConfig(ctx)
	driver := normalizeDriver(req.Driver)
	if driver == "" {
		driver = current.Driver
	}
	if !cacheDrivers[driver] {
		return infraError(c, http.StatusBadRequest, "Unknown cache driver")
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" && driver == current.Driver {
		rawURL = current.URL
	}
	if err := validateConnURL(driver, rawURL); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	latency, err := probeCache(ctx, newConnConfig(driver, rawURL, "request"))
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "driver": driver, "error": safeError(err, rawURL)})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "driver": driver, "latency_ms": latency.Milliseconds()})
}

// POST /api/admin/infra/cache/flush

type cacheFlushRequest struct {
	Scope string `json:"scope"`
}

func (rt *infraRuntime) handleCacheFlush(c echo.Context) error {
	var req cacheFlushRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	namespaces, ok := cacheScopes[req.Scope]
	if !ok {
		return infraError(c, http.StatusBadRequest, "scope must be page, object or all")
	}
	ctx := c.Request().Context()
	store, _, err := rt.activeCache(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Cache is unavailable")
	}
	for _, ns := range namespaces {
		if err := store.Flush(ctx, ns); err != nil {
			return infraError(c, http.StatusInternalServerError, "Cache flush failed")
		}
	}
	syslog.Audit(ctx, infraActor(c), "cache.flush", map[string]any{"scope": req.Scope})
	return c.JSON(http.StatusOK, map[string]any{"scope": req.Scope, "flushed": namespaces})
}

// PUT /api/admin/infra/queue

type queueUpdateRequest struct {
	Driver  *string `json:"driver"`
	URL     *string `json:"url"`
	Workers *int    `json:"workers"`
}

// handleQueuePut changes the connection and worker count of the queue in
// use. The driver itself cannot change here, because moving the jobs is the
// job of the switch endpoint.
func (rt *infraRuntime) handleQueuePut(c echo.Context) error {
	var req queueUpdateRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	current := queueConfig(ctx)
	if req.Driver != nil && normalizeDriver(*req.Driver) != current.Driver {
		return infraError(c, http.StatusConflict, "Use /api/admin/infra/queue/switch to change the queue driver")
	}
	kv := map[string]string{}
	if req.Workers != nil {
		if *req.Workers < 1 || *req.Workers > maxQueueWorkers {
			return infraError(c, http.StatusBadRequest, "workers must be 1 to 64")
		}
		kv[keyQueueWorkers] = strconv.Itoa(*req.Workers)
	}
	if req.URL != nil {
		if os.Getenv(envQueueURL) != "" {
			return infraError(c, http.StatusConflict, "JELLY_QUEUE_URL is set; change the URL in the environment")
		}
		rawURL := strings.TrimSpace(*req.URL)
		if _, takesURL := connSchemes[current.Driver]; takesURL {
			if err := validateConnURL(current.Driver, rawURL); err != nil {
				return infraError(c, http.StatusBadRequest, err.Error())
			}
			if _, err := probeQueue(ctx, newConnConfig(current.Driver, rawURL, "request"), queueWorkers(ctx)); err != nil {
				return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "Connection test failed: " + safeError(err, rawURL)})
			}
		} else if rawURL != "" {
			return infraError(c, http.StatusBadRequest, "The "+current.Driver+" driver takes no connection URL")
		}
		if _, activeCfg, active := systemQueueForDB(data.DB); active && newConnConfig(current.Driver, rawURL, "request").signature() != activeCfg.signature() {
			return infraError(c, http.StatusConflict, "Use a managed queue switch before changing the active queue connection")
		}
		kv[keyQueueURL] = rawURL
	}
	if len(kv) > 0 {
		if err := settings.Set(ctx, infraActor(c), kv); err != nil {
			return settingsWriteError(c, err)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "Queue settings saved", "driver": current.Driver})
}

// POST /api/admin/infra/queue/test

func (rt *infraRuntime) handleQueueTest(c echo.Context) error {
	var req connTestRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	current := queueConfig(ctx)
	driver := normalizeDriver(req.Driver)
	if driver == "" {
		driver = current.Driver
	}
	if !queueDriverAllowed(driver) {
		return infraError(c, http.StatusBadRequest, "Unknown queue driver")
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" && driver == current.Driver {
		rawURL = current.URL
	}
	if err := validateConnURL(driver, rawURL); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	latency, err := probeQueue(ctx, newConnConfig(driver, rawURL, "request"), queueWorkers(ctx))
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "driver": driver, "error": safeError(err, rawURL)})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "driver": driver, "latency_ms": latency.Milliseconds()})
}

// POST /api/admin/infra/queue/switch

type queueSwitchRequest struct {
	To  string  `json:"to"`
	URL *string `json:"url"`
}

// handleQueueSwitch moves every queued job from the active queue to the new
// one, then makes the new one active. A durable journal records the transfer;
// target acceptance precedes the settings cutover, and source confirmation
// follows it. Failed recovery keeps workers stopped until the journal resolves.
func (rt *infraRuntime) handleQueueSwitch(c echo.Context) error {
	var req queueSwitchRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	gateCtx, releaseGate, gateErr := acquireExclusiveSystemWrite(ctx, data.DB)
	if gateErr != nil {
		return infraError(c, http.StatusServiceUnavailable, "Site write coordination is unavailable")
	}
	defer releaseGate()
	ctx = gateCtx
	to := normalizeDriver(req.To)
	if !queueDriverAllowed(to) {
		return infraError(c, http.StatusBadRequest, "Unknown queue driver")
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	from, fromCfg, err := rt.activeQueueLocked(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Current queue is unavailable")
	}
	if to == fromCfg.Driver {
		return infraError(c, http.StatusConflict, "The queue already uses the "+to+" driver")
	}
	_, _, sharedRuntime := systemQueueForDB(data.DB)

	target, source := settings.Get(ctx, keyQueueURL), "settings"
	if env := strings.TrimSpace(os.Getenv(envQueueURL)); env != "" {
		target, source = env, "env"
	} else if req.URL != nil {
		target, source = strings.TrimSpace(*req.URL), "request"
	}
	cfg := newConnConfig(to, target, source)
	if err := validateConnURL(to, cfg.URL); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	next, err := queue.Open(queue.Config{Driver: to, URL: cfg.URL, Workers: queueWorkers(ctx)})
	if err != nil {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": safeError(err, cfg.URL)})
	}
	if _, err := next.Ping(ctx); err != nil {
		_ = next.Close()
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "Connection test failed: " + safeError(err, cfg.URL)})
	}

	if !sharedRuntime {
		stats, err := from.Stats(ctx)
		if err != nil {
			_ = next.Close()
			return infraError(c, http.StatusServiceUnavailable, "Current queue is unavailable")
		}
		running := 0
		for _, s := range stats {
			running += s.Running
		}
		if running > 0 {
			_ = next.Close()
			return infraError(c, http.StatusConflict, "Jobs are running on the current queue; switch when they finish")
		}
	}

	actor := infraActor(c)
	switchQueue := func(active queue.Queue) error {
		stats, statErr := active.Stats(ctx)
		if statErr != nil {
			return statErr
		}
		running := 0
		for _, s := range stats {
			running += s.Running
		}
		if running > 0 {
			return queue.ErrJobRunning
		}
		cutover := map[string]string{keyQueueDriver: to}
		if source == "request" {
			cutover[keyQueueURL] = cfg.URL
		}
		return durableQueueTransfer(ctx, data.DB, active, next, fromCfg, cfg, actor, func() error {
			return settings.Set(ctx, actor, cutover)
		})
	}
	sharedSwitch, switchErr := switchSystemQueue(ctx, data.DB, next, cfg, switchQueue)
	if !sharedSwitch {
		switchErr = switchQueue(from)
	}
	if switchErr != nil {
		_ = next.Close()
		log.Printf("infra: queue switch %s -> %s failed: %s", fromCfg.Driver, to, safeError(switchErr, cfg.URL))
		if errors.Is(switchErr, queue.ErrJobRunning) {
			return infraError(c, http.StatusConflict, "Jobs are running on the current queue; switch when they finish")
		}
		return infraError(c, http.StatusInternalServerError, "Queue switch failed; the previous queue remains authoritative")
	}
	_ = from.Close()
	rt.queue, rt.queueSig = next, cfg.signature()
	syslog.Audit(ctx, actor, "queue.switch", map[string]any{"from": fromCfg.Driver, "to": to})
	return c.JSON(http.StatusAccepted, map[string]any{"from": fromCfg.Driver, "to": to, "state": "done"})
}

// POST /api/admin/infra/queue/pause and /resume

func setQueuePaused(c echo.Context, paused bool) error {
	ctx := c.Request().Context()
	if err := settings.Set(ctx, infraActor(c), map[string]string{keyQueuePaused: strconv.FormatBool(paused)}); err != nil {
		return settingsWriteError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"paused": paused})
}

func handleQueuePause(c echo.Context) error  { return setQueuePaused(c, true) }
func handleQueueResume(c echo.Context) error { return setQueuePaused(c, false) }

// jobView is the admin form of one job. The payload is never shown.
type jobView struct {
	ID          string    `json:"id"`
	Queue       string    `json:"queue"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	RunAt       time.Time `json:"run_at"`
	DedupeKey   string    `json:"dedupe_key,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

func toJobView(info queue.JobInfo) jobView {
	return jobView{
		ID: info.ID, Queue: info.Queue, Kind: info.Kind, State: info.State,
		Attempts: info.Attempts, MaxAttempts: info.MaxAttempts, RunAt: info.RunAt,
		DedupeKey: info.DedupeKey, LastError: info.LastError,
	}
}

// GET /api/admin/jobs?queue=&state=&limit=

func (rt *infraRuntime) handleJobsList(c echo.Context) error {
	ctx := c.Request().Context()
	queueName := strings.TrimSpace(c.QueryParam("queue"))
	if queueName != "" && !queueNamePattern.MatchString(queueName) {
		return infraError(c, http.StatusBadRequest, "Invalid queue name")
	}
	state := strings.TrimSpace(c.QueryParam("state"))
	if state != "" && !jobStates[state] {
		return infraError(c, http.StatusBadRequest, "state must be queued, running, done or failed")
	}
	limit := 100
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return infraError(c, http.StatusBadRequest, "limit must be 1 to 200")
		}
		limit = n
	}
	_, ins, err := rt.inspector(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Queue is unavailable")
	}
	rows, err := ins.List(ctx, queue.ListFilter{Queue: queueName, State: state, Limit: limit})
	if err != nil {
		return infraError(c, http.StatusInternalServerError, "Job list failed")
	}
	items := make([]jobView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toJobView(row))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// POST /api/admin/jobs/:id/retry

func (rt *infraRuntime) handleJobRetry(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	q, ins, err := rt.inspector(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Queue is unavailable")
	}
	info, err := ins.Get(ctx, id)
	switch {
	case errors.Is(err, queue.ErrNotFound):
		return infraError(c, http.StatusNotFound, "Job not found")
	case err != nil:
		return infraError(c, http.StatusInternalServerError, "Job lookup failed")
	}
	if info.State != "failed" {
		return infraError(c, http.StatusConflict, "Only failed jobs can be retried")
	}
	if err := q.Retry(ctx, id); err != nil {
		return infraError(c, http.StatusInternalServerError, "Retry failed")
	}
	syslog.Audit(ctx, infraActor(c), "jobs.retry", map[string]any{"job_id": id, "queue": info.Queue, "kind": info.Kind})
	return c.JSON(http.StatusOK, map[string]any{"id": id, "state": "queued"})
}

// POST /api/admin/jobs/retry-failed?queue=

func (rt *infraRuntime) handleJobsRetryFailed(c echo.Context) error {
	ctx := c.Request().Context()
	queueName := strings.TrimSpace(c.QueryParam("queue"))
	if queueName != "" && !queueNamePattern.MatchString(queueName) {
		return infraError(c, http.StatusBadRequest, "Invalid queue name")
	}
	_, ins, err := rt.inspector(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Queue is unavailable")
	}
	n, err := ins.RetryFailed(ctx, queueName)
	if err != nil {
		return infraError(c, http.StatusInternalServerError, "Retry failed")
	}
	syslog.Audit(ctx, infraActor(c), "jobs.retry_failed", map[string]any{"queue": queueName, "count": n})
	return c.JSON(http.StatusOK, map[string]any{"retried": n})
}

// DELETE /api/admin/jobs/:id

func (rt *infraRuntime) handleJobDelete(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	_, ins, err := rt.inspector(ctx)
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Queue is unavailable")
	}
	switch err := ins.Remove(ctx, id); {
	case errors.Is(err, queue.ErrNotFound):
		return infraError(c, http.StatusNotFound, "Job not found")
	case errors.Is(err, queue.ErrJobRunning):
		return infraError(c, http.StatusConflict, "A running job cannot be deleted")
	case err != nil:
		return infraError(c, http.StatusInternalServerError, "Delete failed")
	}
	syslog.Audit(ctx, infraActor(c), "jobs.delete", map[string]any{"job_id": id})
	return c.JSON(http.StatusOK, map[string]any{"deleted": id})
}

// registerInfraRoutes mounts the infrastructure API. Every route is admin only.
func registerInfraRoutes(e *echo.Echo) {
	rt := newInfraRuntime()
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/infra", rt.handleStatus, admin)
	e.PUT("/api/admin/infra/cache", rt.handleCachePut, admin)
	e.POST("/api/admin/infra/cache/test", rt.handleCacheTest, admin)
	e.POST("/api/admin/infra/cache/flush", rt.handleCacheFlush, admin)
	e.PUT("/api/admin/infra/queue", rt.handleQueuePut, admin)
	e.POST("/api/admin/infra/queue/test", rt.handleQueueTest, admin)
	e.POST("/api/admin/infra/queue/switch", rt.handleQueueSwitch, admin)
	e.POST("/api/admin/infra/queue/pause", handleQueuePause, admin)
	e.POST("/api/admin/infra/queue/resume", handleQueueResume, admin)
	e.GET("/api/admin/jobs", rt.handleJobsList, admin)
	e.POST("/api/admin/jobs/:id/retry", rt.handleJobRetry, admin)
	e.POST("/api/admin/jobs/retry-failed", rt.handleJobsRetryFailed, admin)
	e.DELETE("/api/admin/jobs/:id", rt.handleJobDelete, admin)
}
