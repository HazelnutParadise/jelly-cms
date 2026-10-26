package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type agentEventResponse struct {
	ID            uint            `json:"id"`
	At            time.Time       `json:"at"`
	ActorType     string          `json:"actor_type"`
	ActorUserID   *uint           `json:"actor_user_id"`
	AgentID       uint            `json:"agent_id"`
	AgentName     string          `json:"agent_name"`
	KeyID         *uint           `json:"key_id"`
	KeyPrefix     string          `json:"key_prefix"`
	Action        string          `json:"action"`
	Module        string          `json:"module"`
	Method        string          `json:"method"`
	Route         string          `json:"route"`
	Status        int             `json:"status"`
	IPMasked      string          `json:"ip_masked"`
	Summary       json.RawMessage `json:"summary"`
	CorrelationID string          `json:"correlation_id"`
}

func eventResponse(e core.AgentEvent) agentEventResponse {
	summary := e.Summary
	if len(summary) == 0 {
		summary = json.RawMessage(`{}`)
	}
	return agentEventResponse{e.ID, e.At, e.ActorType, e.ActorUserID, e.AgentID, e.AgentName, e.KeyID, e.KeyPrefix, e.Action, e.Module, e.Method, e.Route, e.Status, e.IPMasked, summary, e.RequestID}
}

func eventCursor(id uint) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(id), 10)))
}
func parseEventCursor(s string) (uint, bool) {
	if s == "" {
		return 0, true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(string(b), 10, 64)
	return uint(n), err == nil && n > 0 && uint64(uint(n)) == n
}

func maskedRealIP(c echo.Context) string {
	raw := c.RealIP()
	host, _, err := net.SplitHostPort(raw)
	if err == nil {
		raw = host
	}
	return agent.MaskIP(raw)
}

func newAgentRequestEvent(c echo.Context, a *core.Agent, key *core.AgentKey) core.AgentEvent {
	path := c.Path()
	if strings.HasSuffix(path, "/*") {
		path = ""
	}
	module := "unknown"
	permission, _ := c.Get("agent.permission").(string)
	if permission != "" {
		module = strings.SplitN(permission, ".", 2)[0]
	} else if strings.HasSuffix(c.Path(), "/me") {
		module = "me"
	}
	params := map[string]string{}
	truncated := false
	if path != "" {
		for _, name := range c.ParamNames() {
			value := c.Param(name)
			if len(value) > 256 {
				value = value[:256]
				truncated = true
			}
			params[name] = value
		}
	}
	queryTotal := 0
	keys := make([]string, 0, 64)
	seenKeys := map[string]bool{}
	rawQuery := c.Request().URL.RawQuery
	for offset := 0; offset < len(rawQuery); {
		end := strings.IndexByte(rawQuery[offset:], '&')
		if end < 0 {
			end = len(rawQuery)
		} else {
			end += offset
		}
		part := rawQuery[offset:end]
		offset = end + 1
		if part == "" {
			continue
		}
		queryTotal++
		name := strings.SplitN(part, "=", 2)[0]
		if len(name) > 96 {
			name = name[:96]
			truncated = true
		}
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if len(name) > 96 {
			name = name[:96]
			truncated = true
		}
		if !seenKeys[name] && len(keys) < 64 {
			keys = append(keys, name)
			seenKeys[name] = true
		} else {
			if !seenKeys[name] {
				truncated = true
			}
		}
	}
	sort.Strings(keys)
	bodyBytes := int64(0)
	if c.Request().ContentLength >= 0 {
		bodyBytes = c.Request().ContentLength
	}
	summary := marshalAgentRequestSummary(params, keys, bodyBytes, truncated, queryTotal)
	return core.AgentEvent{At: time.Now().UTC(), ActorType: "agent", AgentID: a.ID, AgentName: a.Name, KeyID: &key.ID, KeyPrefix: key.Prefix, Action: "request", Module: module, Method: c.Request().Method, Route: path, Status: 0, IPMasked: maskedRealIP(c), Summary: summary, RequestID: requestIDFor(c)}
}

func marshalAgentRequestSummary(params map[string]string, keys []string, bodyBytes int64, truncated bool, queryTotal int) json.RawMessage {
	makeSummary := func() []byte {
		v := map[string]any{"params": params, "query_keys": keys, "body_bytes": bodyBytes}
		if truncated {
			v["truncated"] = true
			v["params_total"] = len(params)
			v["query_keys_total"] = queryTotal
		}
		b, _ := json.Marshal(v)
		return b
	}
	for len(makeSummary()) > 1400 && len(keys) > 0 {
		keys = keys[:len(keys)-1]
		truncated = true
	}
	for len(makeSummary()) > 1400 {
		changed := false
		for name, value := range params {
			if len(value) > 32 {
				params[name] = value[:len(value)-32]
				changed = true
			}
		}
		truncated = true
		if !changed {
			break
		}
	}
	return makeSummary()
}

func writeAgentEvent(tx *gorm.DB, ev core.AgentEvent) error {
	_, err := writeAgentEventWithSystemLog(tx, ev)
	return err
}

type agentAuditRecord struct {
	eventID uint
	logID   uint
}

func writeAgentEventWithSystemLog(tx *gorm.DB, ev core.AgentEvent) (agentAuditRecord, error) {
	var record agentAuditRecord
	if len(ev.Summary) == 0 {
		ev.Summary = json.RawMessage(`{}`)
	}
	if len(ev.Summary) > 2048 {
		return record, fmt.Errorf("agent event summary exceeds 2 KiB")
	}
	if err := tx.Create(&ev).Error; err != nil {
		return record, err
	}
	record.eventID = ev.ID
	fields := map[string]any{"agent_id": ev.AgentID, "key_prefix": ev.KeyPrefix, "action": ev.Action, "module": ev.Module, "route": ev.Route, "status": ev.Status}
	blob, err := json.Marshal(fields)
	if err != nil {
		return record, err
	}
	var actor any
	if ev.ActorUserID != nil {
		actor = *ev.ActorUserID
	}
	err = tx.Raw(`INSERT INTO system_logs (at,level,source,message,fields,request_id,actor_user_id,ip_masked) VALUES (?, ?, ?, ?, CAST(? AS JSONB), ?, ?, ?) RETURNING id`, ev.At, syslog.LevelInfo, syslog.SourceAudit, ev.Action, string(blob), nullableAgentString(ev.RequestID), actor, nullableAgentString(ev.IPMasked)).Scan(&record.logID).Error
	return record, err
}

func createPendingAgentRequest(ev core.AgentEvent) (agentAuditRecord, error) {
	var record agentAuditRecord
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		record, err = writeAgentEventWithSystemLog(tx, ev)
		return err
	})
	return record, err
}

func logAgentAuditFailure(ev core.AgentEvent, eventID, logID uint, phase string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var keyID uint
	if ev.KeyID != nil {
		keyID = *ev.KeyID
	}
	syslog.Emit(ctx, syslog.Entry{Level: syslog.LevelError, Source: syslog.SourceApp, Message: "agent request audit persistence failed", RequestID: ev.RequestID, IPMasked: ev.IPMasked, Fields: map[string]any{"phase": phase, "event_id": eventID, "system_log_id": logID, "agent_id": ev.AgentID, "key_id": keyID}})
}

func finalizePendingAgentRequest(record agentAuditRecord, status int, summary json.RawMessage, requestID string, agentID, keyID uint, ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&core.AgentEvent{}).Where("id = ? AND status = 0", record.eventID).Updates(map[string]any{"status": status, "summary": summary})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("pending agent event update affected %d rows", result.RowsAffected)
		}
		result = tx.Exec(`UPDATE system_logs SET fields = jsonb_set(COALESCE(fields, '{}'::jsonb), '{status}', to_jsonb(?::integer), true) WHERE id = ?`, status, record.logID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("pending system audit update affected %d rows", result.RowsAffected)
		}
		return nil
	})
	if err != nil {
		syslog.Emit(ctx, syslog.Entry{Level: syslog.LevelError, Source: syslog.SourceApp, Message: "agent request audit finalization failed", RequestID: requestID, IPMasked: ip, Fields: map[string]any{"phase": "finalize", "event_id": record.eventID, "system_log_id": record.logID, "agent_id": agentID, "key_id": keyID}})
	}
}

func nullableAgentString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func agentRequestAudit(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		key, ok := c.Get("agent.key").(*core.AgentKey)
		if !ok || key == nil {
			return next(c)
		}
		a, ok := c.Get("agent.identity").(*core.Agent)
		if !ok || a == nil {
			return next(c)
		}
		ev := newAgentRequestEvent(c, a, key)
		declaredLength := c.Request().ContentLength
		counter := &agentBodyCounter{ReadCloser: c.Request().Body}
		if c.Request().Body != nil {
			c.Request().Body = counter
		}
		record, err := createPendingAgentRequest(ev)
		if err != nil {
			logAgentAuditFailure(ev, 0, 0, "insert")
			return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		}
		err = next(c)
		status := c.Response().Status
		if he, ok := err.(*echo.HTTPError); ok {
			status = he.Code
		}
		if status == 0 {
			status = http.StatusOK
		}
		if err != nil && status < 400 {
			status = http.StatusInternalServerError
		}
		bodyBytes := evSummaryBodyBytes(ev.Summary)
		if declaredLength < 0 {
			bodyBytes = counter.n
		}
		ev.Summary = replaceAgentSummaryBodyBytes(ev.Summary, bodyBytes)
		finalizePendingAgentRequest(record, status, ev.Summary, ev.RequestID, a.ID, key.ID, ev.IPMasked)
		return err
	}
}

type agentBodyCounter struct {
	io.ReadCloser
	n int64
}

func (r *agentBodyCounter) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n += int64(n)
	return n, err
}
func evSummaryBodyBytes(raw json.RawMessage) int64 {
	var s struct {
		BodyBytes int64 `json:"body_bytes"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.BodyBytes
}
func replaceAgentSummaryBodyBytes(raw json.RawMessage, n int64) json.RawMessage {
	var s map[string]any
	if json.Unmarshal(raw, &s) != nil {
		return raw
	}
	s["body_bytes"] = n
	b, _ := json.Marshal(s)
	return b
}

func listAgentEvents(c echo.Context, own *uint) error {
	q := c.QueryParams()
	for k := range q {
		switch k {
		case "agent_id", "actor_type", "action", "module", "status", "key_id", "since", "until", "cursor", "limit":
		default:
			return writeAPIError(c, 400, "logs_filter_invalid", "Invalid event filter", nil)
		}
	}
	limit := 50
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return writeAPIError(c, 400, "logs_filter_invalid", "limit must be 1 to 100", nil)
		}
		limit = n
	}
	cursor, ok := parseEventCursor(c.QueryParam("cursor"))
	if !ok {
		return writeAPIError(c, 400, "logs_filter_invalid", "Invalid cursor", nil)
	}
	query := data.DB.WithContext(c.Request().Context()).Model(&core.AgentEvent{})
	if own != nil {
		query = query.Where("agent_id = ? AND actor_type = ?", *own, "agent")
	} else if raw := c.QueryParam("agent_id"); raw != "" {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil || n == 0 {
			return writeAPIError(c, 400, "logs_filter_invalid", "Invalid agent_id", nil)
		}
		query = query.Where("agent_id = ?", n)
	}
	for k, col := range map[string]string{"actor_type": "actor_type", "module": "module"} {
		if v := c.QueryParam(k); v != "" {
			if (k == "actor_type" && v != "agent" && v != "user") || len(v) > 32 {
				return writeAPIError(c, 400, "logs_filter_invalid", "Invalid filter", nil)
			}
			query = query.Where(col+" = ?", v)
		}
	}
	var actions []string
	actionSeen := map[string]bool{}
	for _, action := range q["action"] {
		if action == "" {
			continue
		}
		if len(action) > 32 {
			return writeAPIError(c, 400, "logs_filter_invalid", "Invalid action", nil)
		}
		if !actionSeen[action] {
			actionSeen[action] = true
			actions = append(actions, action)
		}
	}
	if len(actions) > 0 {
		query = query.Where("action IN ?", actions)
	}
	if raw := c.QueryParam("status"); raw != "" {
		var lo, hi int
		if len(raw) == 3 && raw[1:] == "xx" && strings.Contains("2345", raw[:1]) {
			d, _ := strconv.Atoi(raw[:1])
			lo = d * 100
			hi = lo + 99
		} else {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 100 || n > 599 {
				return writeAPIError(c, 400, "logs_filter_invalid", "Invalid status", nil)
			}
			lo = n
			hi = n
		}
		query = query.Where("status BETWEEN ? AND ?", lo, hi)
	}
	if raw := c.QueryParam("key_id"); raw != "" {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil || n == 0 {
			return writeAPIError(c, 400, "logs_filter_invalid", "Invalid key_id", nil)
		}
		query = query.Where("key_id = ?", n)
	}
	for _, item := range []struct{ name, op string }{{"since", ">="}, {"until", "<"}} {
		if raw := c.QueryParam(item.name); raw != "" {
			t, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				return writeAPIError(c, 400, "logs_filter_invalid", "Time must be RFC3339", nil)
			}
			query = query.Where("at "+item.op+" ?", t)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read events", nil)
	}
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	var rows []core.AgentEvent
	if err := query.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read events", nil)
	}
	var next any = nil
	if len(rows) > limit {
		rows = rows[:limit]
		next = eventCursor(rows[len(rows)-1].ID)
	}
	items := make([]agentEventResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, eventResponse(row))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "next_cursor": next, "total": total})
}

func listAgentAdminEvents(c echo.Context) error { return listAgentEvents(c, nil) }
func listAgentOwnEvents(c echo.Context) error {
	id := c.Get("agent.identity").(*core.Agent).ID
	return listAgentEvents(c, &id)
}
