// Package syslog records system logs: the request ID middleware, the access
// log, field redaction, audit lines and retention. Sinks decide where entries
// go. The database sink receives its handle from the server, so this package
// does not import internal/data.
package syslog

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Levels match the system_logs.level column (0–3).
const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

// Sources match the system_logs.source column.
const (
	SourceApp     = "app"
	SourceHTTP    = "http"
	SourcePayment = "payment"
	SourcePlugin  = "plugin"
	SourceAudit   = "audit"
	SourceCron    = "cron"
	SourceJob     = "job"
	SourceCrawler = "crawler"
)

// RequestIDHeader is echoed on every response and read by error pages.
const RequestIDHeader = "X-Request-ID"

// RequestIDContextKey is the echo.Context key holding the request ID.
const RequestIDContextKey = "request_id"

// SlowRequest is the latency above which successful requests are logged.
const SlowRequest = time.Second

// Entry is one system log record.
type Entry struct {
	At          time.Time
	Level       int
	Source      string
	Message     string
	Fields      map[string]any
	RequestID   string
	ActorUserID uint
	PluginID    string
	IPMasked    string
}

// Sink receives every entry emitted through Emit.
type Sink interface {
	Write(ctx context.Context, e Entry) error
}

// DBSink hands each entry to Insert, which the server backs with the database
// it owns. A nil Insert drops the entry.
type DBSink struct {
	Insert func(ctx context.Context, e Entry) error
}

// Write implements Sink.
func (s DBSink) Write(ctx context.Context, e Entry) error {
	if s.Insert == nil {
		return nil
	}
	return s.Insert(ctx, e)
}

// DBWriter returns an Insert function that stores entries in system_logs. The
// database comes from current on each call, so a site that is not installed
// yet (nil handle) silently drops entries.
func DBWriter(current func() *gorm.DB) func(context.Context, Entry) error {
	return func(ctx context.Context, e Entry) error {
		db := current()
		if db == nil {
			return nil
		}
		fields, err := json.Marshal(nonNilFields(e.Fields))
		if err != nil {
			return fmt.Errorf("encode system log fields: %w", err)
		}
		var actor any
		if e.ActorUserID != 0 {
			actor = int64(e.ActorUserID)
		}
		return db.WithContext(ctx).Exec(`INSERT INTO system_logs
			(at, level, source, message, fields, request_id, actor_user_id, plugin_id, ip_masked)
			VALUES (?, ?, ?, ?, CAST(? AS JSONB), ?, ?, ?, ?)`,
			e.At, e.Level, e.Source, e.Message, string(fields),
			nullableString(e.RequestID), actor, nullableString(e.PluginID), nullableString(maskIP(e.IPMasked)),
		).Error
	}
}

func nonNilFields(fields map[string]any) map[string]any {
	if fields == nil {
		return map[string]any{}
	}
	return fields
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// stdoutSink prints one line per entry through the standard logger.
type stdoutSink struct{}

func (stdoutSink) Write(_ context.Context, e Entry) error {
	log.Printf("syslog level=%d source=%s request_id=%s actor=%d msg=%q fields=%v",
		e.Level, e.Source, e.RequestID, e.ActorUserID, e.Message, e.Fields)
	return nil
}

// StdoutSink returns the sink that prints entries through the standard logger.
func StdoutSink() Sink { return stdoutSink{} }

var (
	sinkMu sync.RWMutex
	sinks  = []Sink{stdoutSink{}}
)

// SetSinks replaces the sinks. The server calls it when routes are registered.
func SetSinks(next ...Sink) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sinks = append([]Sink(nil), next...)
}

// Emit redacts fields and masks the client address, stamps the time and writes
// the entry to every sink. Sink errors are logged and never returned.
func Emit(ctx context.Context, e Entry) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if e.Source == "" {
		e.Source = SourceApp
	}
	e.Fields = Redact(e.Fields)
	e.IPMasked = maskIP(e.IPMasked)
	sinkMu.RLock()
	defer sinkMu.RUnlock()
	for _, s := range sinks {
		if err := s.Write(ctx, e); err != nil {
			log.Printf("syslog: write entry: %v", err)
		}
	}
}

// Audit emits an audit entry naming the actor and the action. Only field
// names are safe to log for settings writes, so callers pass keys, not values.
func Audit(ctx context.Context, actor uint, action string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["action"] = action
	Emit(ctx, Entry{Level: LevelInfo, Source: SourceAudit, Message: action, Fields: fields, ActorUserID: actor})
}

// sensitiveField matches field names that carry secrets. A bare "key" or a
// name ending in "_key" matches, but "keys" (used by the settings audit to
// name the keys written) does not.
var sensitiveField = regexp.MustCompile(`(?i)password|secret|token|authorization|cookie|hash|(^|_)key$`)

// addressField matches field names that carry a client address.
var addressField = regexp.MustCompile(`(?i)(^|_)(ip|ip_address|remote_addr|remote_ip|client_ip)$`)

// Redact returns a copy of fields with values of sensitive names replaced and
// client addresses reduced to their network (see maskIP). Names are matched
// case-insensitively.
func Redact(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if sensitiveField.MatchString(k) {
			out[k] = "[redacted]"
			continue
		}
		if addressField.MatchString(k) {
			if masked := maskIP(fmt.Sprint(v)); masked != "" {
				out[k] = masked
			} else {
				out[k] = "[redacted]"
			}
			continue
		}
		out[k] = v
	}
	return out
}

// maskIP reduces an address to its network: IPv4 keeps the first three octets
// (/24), IPv6 keeps the first 48 bits (/48). Anything that is not an address
// or CIDR block becomes "" so it is never stored.
func maskIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		parsed, _, err := net.ParseCIDR(raw)
		if err != nil {
			return ""
		}
		ip = parsed
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.0/24", v4[0], v4[1], v4[2])
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}

var requestIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewRequestID returns "req_" followed by 20 lowercase base32 characters
// (100 bits from crypto/rand).
func NewRequestID() string {
	var buf [13]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand does not fail on supported platforms; keep the ID shape anyway.
		return "req_00000000000000000000"
	}
	id := strings.ToLower(requestIDEncoding.EncodeToString(buf[:]))
	return "req_" + id[:20]
}

// RequestID assigns a fresh request ID to every request, stores it in the
// context under RequestIDContextKey and echoes it in X-Request-ID. Incoming
// IDs are not trusted and are replaced.
func RequestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			id := NewRequestID()
			c.Set(RequestIDContextKey, id)
			c.Response().Header().Set(RequestIDHeader, id)
			return next(c)
		}
	}
}

// AccessLog is the HTTP access-log middleware. It records requests that fail
// (status 500 and up as errors, 400 and up as warnings) and requests slower
// than SlowRequest. Other requests are not logged. Only the route pattern is
// recorded, never the query string. It must run after RequestID.
func AccessLog() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			elapsed := time.Since(start)
			status := c.Response().Status
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
				} else {
					status = 500
				}
			}
			level := LevelInfo
			switch {
			case status >= 500:
				level = LevelError
			case status >= 400:
				level = LevelWarn
			case elapsed <= SlowRequest:
				return err
			}
			reqID, _ := c.Get(RequestIDContextKey).(string)
			path := c.Path()
			if path == "" {
				path = c.Request().URL.Path
			}
			Emit(c.Request().Context(), Entry{
				Level:     level,
				Source:    SourceHTTP,
				Message:   c.Request().Method + " " + path,
				RequestID: reqID,
				IPMasked:  c.RealIP(),
				Fields: map[string]any{
					"status":     status,
					"latency_ms": elapsed.Milliseconds(),
				},
			})
			return err
		}
	}
}

// Prune deletes system_logs rows past their retention window: warn and info
// rows after infoDays, error rows after errorDays. It returns the number of
// rows removed.
func Prune(ctx context.Context, db *gorm.DB, now time.Time, infoDays, errorDays int) (int64, error) {
	if db == nil {
		return 0, errors.New("syslog: database is not initialized")
	}
	if infoDays <= 0 || errorDays <= 0 {
		return 0, errors.New("syslog: retention days must be positive")
	}
	infoCutoff := now.AddDate(0, 0, -infoDays)
	errorCutoff := now.AddDate(0, 0, -errorDays)
	var removed int64
	res := db.WithContext(ctx).Exec("DELETE FROM system_logs WHERE level < ? AND at < ?", LevelError, infoCutoff)
	if res.Error != nil {
		return removed, fmt.Errorf("prune info logs: %w", res.Error)
	}
	removed += res.RowsAffected
	res = db.WithContext(ctx).Exec("DELETE FROM system_logs WHERE level >= ? AND at < ?", LevelError, errorCutoff)
	if res.Error != nil {
		return removed, fmt.Errorf("prune error logs: %w", res.Error)
	}
	removed += res.RowsAffected
	return removed, nil
}
