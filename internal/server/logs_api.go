package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Log job kind, run daily from cmd/server.
const logsPruneKind = "logs.prune"

// Query limits for the log API (ticket 08: 200 rows per query).
const (
	logsDefaultLimit = 100
	logsMaxLimit     = 200
	logsMaxExportHrs = 720
	logsStreamBatch  = 200
	logsStreamPoll   = 2 * time.Second
	logsStreamBeat   = 15 * time.Second
)

// logsSources lists the values system_logs.source accepts.
var logsSources = map[string]bool{
	syslog.SourceApp: true, syslog.SourceHTTP: true, syslog.SourcePayment: true,
	syslog.SourcePlugin: true, syslog.SourceAudit: true, syslog.SourceCron: true,
	syslog.SourceJob: true, syslog.SourceCrawler: true,
}

// logsLevelNames maps the level words accepted in q ("level:error").
var logsLevelNames = map[string]int{
	"debug": syslog.LevelDebug, "info": syslog.LevelInfo,
	"warn": syslog.LevelWarn, "error": syslog.LevelError,
}

// logsColumns is the select list shared by every log query.
const logsColumns = "id, at, level, source, message, COALESCE(fields::text, '{}') AS fields, request_id, actor_user_id, plugin_id, ip_masked"

func init() {
	queue.Register(logsPruneKind, func(ctx context.Context, _ queue.Job) error {
		_, err := PruneSystemLogs(ctx, data.DB, time.Now().UTC())
		return err
	})
}

// wireLogSinks sends every syslog entry to stdout and to system_logs.
// RegisterRoutes calls it, so the sink is in place wherever the server runs.
func wireLogSinks() {
	syslog.SetSinks(syslog.StdoutSink(), syslog.DBSink{
		Insert: syslog.DBWriter(func() *gorm.DB { return data.DB }),
	})
}

// PruneSystemLogs removes rows past their retention: info and warn rows after
// logs_retention_days (30), error rows after logs_error_retention_days (90).
// It does nothing while the database is not ready.
func PruneSystemLogs(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	infoDays := retentionDays(ctx, "logs_retention_days")
	errorDays := retentionDays(ctx, "logs_error_retention_days")
	return syslog.Prune(ctx, db, now, infoDays, errorDays)
}

// retentionDays reads a retention option, falling back to the registered default.
func retentionDays(ctx context.Context, key string) int {
	spec, _ := settings.Lookup(key)
	days, err := strconv.Atoi(settings.Get(ctx, key))
	if err != nil || days <= 0 {
		days, _ = strconv.Atoi(spec.Default)
	}
	return days
}

// registerLogRoutes mounts the admin log API. Export is also interactive only.
func registerLogRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/logs", handleLogsList, admin)
	e.GET("/api/admin/logs/stream", handleLogsStream, admin)
	e.GET("/api/admin/logs/export", handleLogsExport, auth.RequireInteractive(), admin)
	e.GET("/api/admin/logs/summary", handleLogsSummary, admin)
}

// logRow is one system_logs row as read back.
type logRow struct {
	ID          int64     `gorm:"column:id"`
	At          time.Time `gorm:"column:at"`
	Level       int       `gorm:"column:level"`
	Source      string    `gorm:"column:source"`
	Message     string    `gorm:"column:message"`
	Fields      string    `gorm:"column:fields"`
	RequestID   *string   `gorm:"column:request_id"`
	ActorUserID *int64    `gorm:"column:actor_user_id"`
	PluginID    *string   `gorm:"column:plugin_id"`
	IPMasked    *string   `gorm:"column:ip_masked"`
}

// logItem is the JSON form of a log row.
type logItem struct {
	ID          int64           `json:"id"`
	At          time.Time       `json:"at"`
	Level       int             `json:"level"`
	Source      string          `json:"source"`
	Message     string          `json:"message"`
	Fields      json.RawMessage `json:"fields"`
	RequestID   string          `json:"request_id,omitempty"`
	ActorUserID int64           `json:"actor_user_id,omitempty"`
	PluginID    string          `json:"plugin_id,omitempty"`
	IPMasked    string          `json:"ip_masked,omitempty"`
}

func toLogItem(row logRow) logItem {
	item := logItem{
		ID: row.ID, At: row.At, Level: row.Level, Source: row.Source,
		Message: row.Message, Fields: json.RawMessage(row.Fields),
	}
	if row.RequestID != nil {
		item.RequestID = *row.RequestID
	}
	if row.ActorUserID != nil {
		item.ActorUserID = *row.ActorUserID
	}
	if row.PluginID != nil {
		item.PluginID = *row.PluginID
	}
	if row.IPMasked != nil {
		item.IPMasked = *row.IPMasked
	}
	return item
}

// logCond is one WHERE clause on system_logs.
type logCond struct {
	sql  string
	args []any
}

// logFilter is the parsed query of the list and stream endpoints.
type logFilter struct {
	conds []logCond
	limit int
}

func (f *logFilter) add(sql string, args ...any) {
	f.conds = append(f.conds, logCond{sql: sql, args: args})
}

// apply adds the filter's conditions to q.
func (f logFilter) apply(q *gorm.DB) *gorm.DB {
	for _, cond := range f.conds {
		q = q.Where(cond.sql, cond.args...)
	}
	return q
}

// logLikeEscaper escapes LIKE wildcards so q matches text literally.
var logLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// parseLogFilter reads source, level, q (with "level:name" words), since,
// before_id and limit. Unknown values answer 400; limits above 200 are capped.
func parseLogFilter(c echo.Context) (logFilter, error) {
	f := logFilter{limit: logsDefaultLimit}
	if source := strings.TrimSpace(c.QueryParam("source")); source != "" {
		if !logsSources[source] {
			return f, fmt.Errorf("unknown source %q", source)
		}
		f.add("source = ?", source)
	}
	if raw := strings.TrimSpace(c.QueryParam("level")); raw != "" {
		level, err := strconv.Atoi(raw)
		if err != nil || level < syslog.LevelDebug || level > syslog.LevelError {
			return f, fmt.Errorf("level must be 0 to 3")
		}
		f.add("level = ?", level)
	}
	var words []string
	for _, token := range strings.Fields(c.QueryParam("q")) {
		if name, ok := strings.CutPrefix(strings.ToLower(token), "level:"); ok {
			level, known := logsLevelNames[name]
			if !known {
				return f, fmt.Errorf("unknown level %q", name)
			}
			f.add("level = ?", level)
			continue
		}
		words = append(words, token)
	}
	if len(words) > 0 {
		pattern := "%" + logLikeEscaper.Replace(strings.Join(words, " ")) + "%"
		f.add("(request_id ILIKE ? OR message ILIKE ?)", pattern, pattern)
	}
	if raw := strings.TrimSpace(c.QueryParam("since")); raw != "" {
		since, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return f, fmt.Errorf("since must be RFC 3339")
		}
		f.add("at >= ?", since.UTC())
	}
	if raw := strings.TrimSpace(c.QueryParam("before_id")); raw != "" {
		beforeID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || beforeID <= 0 {
			return f, fmt.Errorf("before_id must be a positive integer")
		}
		f.add("id < ?", beforeID)
	}
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			f.limit = min(n, logsMaxLimit)
		}
	}
	return f, nil
}

// handleLogsList returns the newest matching rows first.
func handleLogsList(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	f, err := parseLogFilter(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	var rows []logRow
	query := f.apply(data.DB.WithContext(c.Request().Context()).Table("system_logs").Select(logsColumns))
	if err := query.Order("id DESC").Limit(f.limit).Scan(&rows).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Log query failed"})
	}
	items := make([]logItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toLogItem(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == f.limit && len(rows) > 0 {
		response["next_before_id"] = rows[len(rows)-1].ID
	}
	return c.JSON(http.StatusOK, response)
}

// handleLogsStream is a server-sent event feed of new rows that match the
// filter. It starts at the newest row, polls the table, and sends a comment
// line every 15 seconds so idle proxies keep the connection open.
func handleLogsStream(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	f, err := parseLogFilter(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	ctx := c.Request().Context()
	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "text/event-stream; charset=utf-8")
	res.Header().Set("Cache-Control", "no-cache")
	res.Header().Set("Connection", "keep-alive")
	res.Header().Set("X-Accel-Buffering", "no")
	res.WriteHeader(http.StatusOK)
	res.Flush()

	var lastID int64
	if err := data.DB.WithContext(ctx).Raw("SELECT COALESCE(MAX(id), 0) FROM system_logs").Scan(&lastID).Error; err != nil {
		log.Printf("logs stream: read tail: %v", err)
	}
	poll := time.NewTicker(logsStreamPoll)
	defer poll.Stop()
	beat := time.NewTicker(logsStreamBeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-beat.C:
			fmt.Fprint(res, ": ping\n\n")
			res.Flush()
		case <-poll.C:
			var rows []logRow
			query := f.apply(data.DB.WithContext(ctx).Table("system_logs").Select(logsColumns)).Where("id > ?", lastID)
			if err := query.Order("id ASC").Limit(logsStreamBatch).Scan(&rows).Error; err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("logs stream: poll: %v", err)
				continue
			}
			for _, row := range rows {
				payload, err := json.Marshal(toLogItem(row))
				if err != nil {
					continue
				}
				fmt.Fprintf(res, "id: %d\nevent: log\ndata: %s\n\n", row.ID, payload)
				lastID = row.ID
			}
			if len(rows) > 0 {
				res.Flush()
			}
		}
	}
}

// handleLogsExport streams the last hours of logs as NDJSON (default 24, at
// most 720). It is interactive only: a login cookie is accepted, while a bearer
// JWT and access keys (jly_, jla_) are refused by RequireInteractive (ticket 23).
func handleLogsExport(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	hours := 24
	if raw := strings.TrimSpace(c.QueryParam("hours")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > logsMaxExportHrs {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "hours must be 1 to 720"})
		}
		hours = n
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	ctx := c.Request().Context()
	rows, err := data.DB.WithContext(ctx).Table("system_logs").Select(logsColumns).
		Where("at >= ?", since).Order("at ASC, id ASC").Rows()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Log export failed"})
	}
	defer rows.Close()

	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "application/x-ndjson; charset=utf-8")
	res.Header().Set(echo.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="jelly-logs-%s.ndjson"`, time.Now().UTC().Format("20060102-150405")))
	res.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(res)
	for rows.Next() {
		var row logRow
		if err := data.DB.ScanRows(rows, &row); err != nil {
			log.Printf("logs export: scan: %v", err)
			return nil
		}
		if err := encoder.Encode(toLogItem(row)); err != nil {
			return nil
		}
	}
	return nil
}

// handleLogsSummary counts the last 24 hours by level and by source for the
// summary cards.
func handleLogsSummary(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	const windowHours = 24
	since := time.Now().UTC().Add(-windowHours * time.Hour)
	db := data.DB.WithContext(c.Request().Context())

	type levelCount struct {
		Level int   `gorm:"column:level"`
		N     int64 `gorm:"column:n"`
	}
	type sourceCount struct {
		Source string `gorm:"column:source"`
		N      int64  `gorm:"column:n"`
	}
	var levels []levelCount
	if err := db.Table("system_logs").Select("level, COUNT(*) AS n").Where("at >= ?", since).Group("level").Scan(&levels).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Log summary failed"})
	}
	var sources []sourceCount
	if err := db.Table("system_logs").Select("source, COUNT(*) AS n").Where("at >= ?", since).Group("source").Scan(&sources).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Log summary failed"})
	}

	byLevel := map[string]int64{"debug": 0, "info": 0, "warn": 0, "error": 0}
	var total int64
	for _, lc := range levels {
		total += lc.N
		for name, level := range logsLevelNames {
			if level == lc.Level {
				byLevel[name] = lc.N
			}
		}
	}
	bySource := map[string]int64{}
	for _, sc := range sources {
		bySource[sc.Source] = sc.N
	}
	return c.JSON(http.StatusOK, map[string]any{
		"window_hours": windowHours,
		"total":        total,
		"by_level":     byLevel,
		"by_source":    bySource,
	})
}

// errorBodyCapture holds a response body so the error handler's output can be
// changed before it reaches the client. Headers still go to the real writer.
type errorBodyCapture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *errorBodyCapture) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *errorBodyCapture) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

// withRequestIDInErrorBody wraps the error handler so plain-text error bodies
// also show the request ID. HTML error pages already show it, and the
// fallback text used when no theme is active did not. The ID is taken from
// the X-Request-ID header set by syslog.RequestID.
func withRequestIDInErrorBody(inner echo.HTTPErrorHandler) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		res := c.Response()
		if res.Committed {
			inner(err, c)
			return
		}
		real := res.Writer
		capture := &errorBodyCapture{ResponseWriter: real}
		res.Writer = capture
		inner(err, c)
		res.Writer = real
		if capture.status == 0 {
			return
		}
		body := capture.body.Bytes()
		if strings.HasPrefix(real.Header().Get(echo.HeaderContentType), echo.MIMETextPlain) && capture.status >= http.StatusBadRequest {
			if id := real.Header().Get(echo.HeaderXRequestID); id != "" {
				body = append(append([]byte{}, body...), []byte("\nrequest_id: "+id+"\n")...)
			}
		}
		real.WriteHeader(capture.status)
		_, _ = real.Write(body)
	}
}
