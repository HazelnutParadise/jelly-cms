package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
)

// logsRowByMessage reads the first system_logs row written with message.
func logsRowByMessage(t *testing.T, h *accessHarness, message string) (level int, source, fields, ipMasked string, found bool) {
	t.Helper()
	var got []struct {
		Level    int
		Source   string
		Fields   sql.NullString
		IPMasked sql.NullString
	}
	if err := h.db.Raw(`SELECT level, source, fields::text AS fields, ip_masked AS ip_masked FROM system_logs WHERE message = ? ORDER BY id`, message).Scan(&got).Error; err != nil {
		t.Fatalf("read system_logs row %q: %v", message, err)
	}
	if len(got) == 0 {
		return 0, "", "", "", false
	}
	row := got[0]
	return row.Level, row.Source, row.Fields.String, row.IPMasked.String, true
}

// logsItems decodes a list response that is either a bare array or {"items": [...]}.
func logsItems(t *testing.T, body string) []map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err == nil {
		return list
	}
	var wrapped struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &wrapped); err != nil {
		t.Fatalf("decode log list: %v; body=%q", err, settingsTruncate(body))
	}
	return wrapped.Items
}

func TestLogsSinkPersistsAndRedacts(t *testing.T) {
	h := newAccessHarness(t)
	const message = "sink probe"

	syslog.Emit(context.Background(), syslog.Entry{
		Level:    syslog.LevelInfo,
		Source:   syslog.SourceApp,
		Message:  message,
		IPMasked: "203.0.113.0/24",
		Fields: map[string]any{
			"password":      "hunter2-probe",
			"authorization": "Bearer probe-token",
			"cookie":        "sid=probe-cookie",
			"ip":            "203.0.113.45",
		},
	})

	level, source, fields, ipMasked, found := logsRowByMessage(t, h, message)
	if !found {
		t.Fatalf("emitted app log %q was not stored in system_logs", message)
	}
	if level != syslog.LevelInfo || source != syslog.SourceApp {
		t.Errorf("stored level=%d source=%q, want level=%d source=%q", level, source, syslog.LevelInfo, syslog.SourceApp)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(fields), &decoded); err != nil {
		t.Fatalf("stored fields are not JSON: %v; fields=%q", err, fields)
	}
	for _, name := range []string{"password", "authorization", "cookie"} {
		if decoded[name] != "[redacted]" {
			t.Errorf("stored field %s=%v, want [redacted]", name, decoded[name])
		}
	}
	for _, secret := range []string{"hunter2-probe", "probe-token", "probe-cookie"} {
		if strings.Contains(fields, secret) {
			t.Errorf("stored fields still contain %q", secret)
		}
	}
	if strings.Contains(fields, "203.0.113.45") || strings.Contains(ipMasked, "203.0.113.45") {
		t.Errorf("stored log keeps the full client IP; fields=%q ip_masked=%q", fields, ipMasked)
	}
	if ipMasked == "" {
		t.Errorf("stored ip_masked is empty, want the masked address")
	}
}

func TestLogsRequestIDOnEveryResponseAndErrorPage(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "logs-request-admin")
	token := h.token(admin)

	ok := h.request(http.MethodGet, "/api/oauth/status", "", "", "application/json", "")
	h.mustNoPanic(ok, "GET /api/oauth/status")
	okID := ok.header.Get("X-Request-ID")
	if ok.code != http.StatusOK || !strings.HasPrefix(okID, "req_") {
		t.Errorf("GET /api/oauth/status returned %d with X-Request-ID=%q, want 200 and a req_ id", ok.code, okID)
	}

	page := h.request(http.MethodGet, "/logs-probe-no-such-page", "", "", "text/html", "")
	h.mustNoPanic(page, "GET public 404")
	pageID := page.header.Get("X-Request-ID")
	if page.code != http.StatusNotFound {
		t.Errorf("GET /logs-probe-no-such-page returned %d, want 404", page.code)
	}
	if !strings.HasPrefix(pageID, "req_") {
		t.Errorf("public 404 X-Request-ID=%q, want a req_ id", pageID)
	} else if !strings.Contains(page.body, pageID) {
		t.Errorf("public 404 page does not show its request ID %q", pageID)
	}

	adminPage := h.request(http.MethodGet, "/admin/logs-probe-no-such-page", "", token, "text/html", "")
	h.mustNoPanic(adminPage, "GET admin 404")
	adminID := adminPage.header.Get("X-Request-ID")
	if !strings.HasPrefix(adminID, "req_") {
		t.Errorf("admin 404 X-Request-ID=%q, want a req_ id", adminID)
	} else if !strings.Contains(adminPage.body, adminID) {
		t.Errorf("admin 404 page does not show its request ID %q", adminID)
	}

	if okID != "" && pageID != "" && okID == pageID {
		t.Errorf("two requests share the request ID %q", okID)
	}
}

func TestLogsAPIFiltersAndLimit(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "logs-api-admin")
	editor := h.user("editor", "logs-api-editor")
	adminToken, editorToken := h.token(admin), h.token(editor)

	if err := h.db.Exec(`INSERT INTO system_logs (at, level, source, message, fields)
		SELECT now() - interval '1 hour', 1, 'app', 'bulk', '{}'::jsonb FROM generate_series(1, 250)`).Error; err != nil {
		t.Fatalf("seed bulk logs: %v", err)
	}
	seed := []struct {
		at      string
		level   int
		source  string
		message string
	}{
		{"now()", 3, "http", "http-error-probe"},
		{"now() - interval '3 days'", 1, "http", "http-info-probe"},
		{"now()", 3, "app", "app-error-probe"},
	}
	for _, s := range seed {
		if err := h.db.Exec(fmt.Sprintf(`INSERT INTO system_logs (at, level, source, message, fields) VALUES (%s, ?, ?, ?, '{}'::jsonb)`, s.at), s.level, s.source, s.message).Error; err != nil {
			t.Fatalf("seed %s: %v", s.message, err)
		}
	}

	get := func(t *testing.T, query string) []map[string]any {
		t.Helper()
		result := h.request(http.MethodGet, "/api/admin/logs?"+query, "", adminToken, "application/json", "")
		h.mustNoPanic(result, "GET /api/admin/logs?"+query)
		if result.code != http.StatusOK {
			t.Fatalf("GET /api/admin/logs?%s returned %d, want 200; body=%q", query, result.code, settingsTruncate(result.body))
		}
		return logsItems(t, result.body)
	}
	messages := func(items []map[string]any) string {
		var out []string
		for _, item := range items {
			out = append(out, fmt.Sprint(item["message"]))
		}
		return strings.Join(out, ",")
	}

	t.Run("source", func(t *testing.T) {
		items := get(t, "source=http")
		if len(items) == 0 {
			t.Fatalf("source=http returned no rows")
		}
		for _, item := range items {
			if item["source"] != "http" {
				t.Errorf("source=http returned a %v row", item["source"])
			}
		}
	})

	t.Run("level", func(t *testing.T) {
		items := get(t, "level=3")
		for _, item := range items {
			if fmt.Sprint(item["level"]) != "3" {
				t.Errorf("level=3 returned a level %v row", item["level"])
			}
		}
		if !strings.Contains(messages(items), "http-error-probe") {
			t.Errorf("level=3 omitted http-error-probe; messages=%s", messages(items))
		}
	})

	t.Run("q level:error", func(t *testing.T) {
		items := get(t, "q="+url.QueryEscape("level:error"))
		if len(items) == 0 {
			t.Fatalf("q=level:error returned no rows")
		}
		for _, item := range items {
			if fmt.Sprint(item["level"]) != "3" {
				t.Errorf("q=level:error returned a level %v row", item["level"])
			}
		}
	})

	t.Run("since", func(t *testing.T) {
		since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
		items := get(t, "since="+url.QueryEscape(since))
		if strings.Contains(messages(items), "http-info-probe") {
			t.Errorf("since=24h still returned the three-day-old row")
		}
		if !strings.Contains(messages(items), "http-error-probe") {
			t.Errorf("since=24h omitted the recent error row")
		}
	})

	t.Run("limit cap", func(t *testing.T) {
		if got := len(get(t, "limit=500")); got != 200 {
			t.Errorf("limit=500 returned %d rows, want the cap of 200", got)
		}
		if got := len(get(t, "limit=2")); got != 2 {
			t.Errorf("limit=2 returned %d rows, want 2", got)
		}
	})

	t.Run("before_id", func(t *testing.T) {
		var maxID int64
		if err := h.db.Raw("SELECT MAX(id) FROM system_logs").Scan(&maxID).Error; err != nil {
			t.Fatalf("read max log id: %v", err)
		}
		items := get(t, fmt.Sprintf("before_id=%d&limit=5", maxID))
		if len(items) == 0 {
			t.Fatalf("before_id=%d returned no rows", maxID)
		}
		for _, item := range items {
			id, ok := item["id"].(float64)
			if !ok || int64(id) >= maxID {
				t.Errorf("before_id=%d returned id %v", maxID, item["id"])
			}
		}
	})

	t.Run("admin only", func(t *testing.T) {
		result := h.request(http.MethodGet, "/api/admin/logs", "", editorToken, "application/json", "")
		h.mustNoPanic(result, "editor GET /api/admin/logs")
		if result.code != http.StatusForbidden {
			t.Errorf("editor GET /api/admin/logs returned %d, want 403", result.code)
		}
	})
}

func TestLogsStreamIsAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "logs-stream-admin")
	editor := h.user("editor", "logs-stream-editor")

	editorResult := h.request(http.MethodGet, "/api/admin/logs/stream", "", h.token(editor), "text/event-stream", "")
	h.mustNoPanic(editorResult, "editor GET /api/admin/logs/stream")
	if editorResult.code != http.StatusForbidden {
		t.Errorf("editor GET /api/admin/logs/stream returned %d, want 403", editorResult.code)
	}

	// The stream never ends, so it is read over a real connection: the headers
	// arrive first, then the body is closed.
	server := httptest.NewServer(h.echo)
	t.Cleanup(server.Close)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/admin/logs/stream", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token(admin))
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("admin GET /api/admin/logs/stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin GET /api/admin/logs/stream returned %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("admin stream Content-Type=%q, want text/event-stream", got)
	}
}

func TestLogsRetentionPrune(t *testing.T) {
	h := newAccessHarness(t)
	if err := h.db.Create(&[]core.Option{
		{Key: "logs_retention_days", Value: "30"},
		{Key: "logs_error_retention_days", Value: "90"},
	}).Error; err != nil {
		t.Fatalf("seed retention options: %v", err)
	}
	rows := []struct {
		at      string
		level   int
		message string
	}{
		{"now() - interval '40 days'", 1, "retention-old-info"},
		{"now() - interval '1 day'", 1, "retention-recent-info"},
		{"now() - interval '40 days'", 3, "retention-old-error"},
		{"now() - interval '100 days'", 3, "retention-ancient-error"},
	}
	for _, r := range rows {
		if err := h.db.Exec(fmt.Sprintf(`INSERT INTO system_logs (at, level, source, message, fields) VALUES (%s, ?, 'app', ?, '{}'::jsonb)`, r.at), r.level, r.message).Error; err != nil {
			t.Fatalf("seed %s: %v", r.message, err)
		}
	}

	handler, ok := queue.HandlerFor("logs.prune")
	if !ok {
		t.Fatalf("job kind logs.prune has no registered handler")
	}
	if err := handler(context.Background(), queue.Job{Queue: queue.QueueSchedule, Kind: "logs.prune"}); err != nil {
		t.Fatalf("logs.prune handler returned %v", err)
	}

	want := map[string]bool{
		"retention-old-info":      false,
		"retention-recent-info":   true,
		"retention-old-error":     true,
		"retention-ancient-error": false,
	}
	for message, keep := range want {
		var count int64
		if err := h.db.Raw("SELECT COUNT(*) FROM system_logs WHERE message = ?", message).Scan(&count).Error; err != nil {
			t.Fatalf("count %s: %v", message, err)
		}
		if keep && count != 1 {
			t.Errorf("%s was pruned; info rows keep 30 days and error rows keep 90", message)
		}
		if !keep && count != 0 {
			t.Errorf("%s survived the prune job", message)
		}
	}
}

func TestLogsExportInteractiveOnly(t *testing.T) {
	h := newAccessHarness(t)

	// A bearer string that no principal recognises must be refused outright.
	unknown := h.request(http.MethodGet, "/api/admin/logs/export?hours=24", "", "jly_x", "application/json", "")
	h.mustNoPanic(unknown, "GET /api/admin/logs/export with unknown token")
	if unknown.code != http.StatusUnauthorized {
		t.Errorf("GET /api/admin/logs/export with Bearer jly_x returned %d, want 401", unknown.code)
	}

	// The API-token principal comes from ticket 06, which is not in this tree.
	// The 403 assertion for a valid token is pending that ticket.
	t.Skip("ticket 06 API token principal is absent; the 403 for token-authenticated log export is pending ticket 06")
}
