// Package syslog records system logs: the request ID middleware, the access
// log, field redaction and audit lines. Sinks decide where entries go.
//
// Shared with ticket 08 (system logs): the DB sink is a stub until the
// system_logs table (migration 0018) is written by the logs service.
package syslog

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
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

// DBSink will write rows to system_logs. It is a stub: entries are dropped
// here and the stdout sink below keeps them visible until the table is wired.
type DBSink struct{}

// Write implements Sink. It stores nothing yet.
func (DBSink) Write(context.Context, Entry) error { return nil }

// stdoutSink prints one line per entry through the standard logger.
type stdoutSink struct{}

func (stdoutSink) Write(_ context.Context, e Entry) error {
	log.Printf("syslog level=%d source=%s request_id=%s actor=%d msg=%q fields=%v",
		e.Level, e.Source, e.RequestID, e.ActorUserID, e.Message, e.Fields)
	return nil
}

var (
	sinkMu sync.RWMutex
	sinks  = []Sink{stdoutSink{}, DBSink{}}
)

// SetSinks replaces the sinks. Tests and the logs service use it.
func SetSinks(next ...Sink) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sinks = append([]Sink(nil), next...)
}

// Emit redacts fields, stamps the time and writes the entry to every sink.
// Sink errors are logged and never returned to the caller.
func Emit(ctx context.Context, e Entry) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.Fields = Redact(e.Fields)
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

// Redact returns a copy of fields with values of sensitive names replaced.
// Names are matched case-insensitively; see sensitiveField.
// TODO(08b): also mask IP addresses and strip query strings from paths.
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
		out[k] = v
	}
	return out
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

// SlowRequest is the latency above which successful requests are logged.
const SlowRequest = time.Second

// AccessLog is the HTTP access-log middleware. It records requests that fail
// (status 500 and up as errors, 400 and up as warnings) and requests slower
// than SlowRequest. Other requests are not logged.
// TODO(08a): wire into RegisterRoutes once the logs service reads the rows.
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
			Emit(c.Request().Context(), Entry{
				Level:     level,
				Source:    SourceHTTP,
				Message:   c.Request().Method + " " + c.Path(),
				RequestID: reqID,
				Fields: map[string]any{
					"status":     status,
					"latency_ms": elapsed.Milliseconds(),
				},
			})
			return err
		}
	}
}
