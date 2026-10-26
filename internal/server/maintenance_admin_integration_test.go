package server

// Ticket 08b (docs/tickets/08-system.md): maintenance mode as the admin manages it,
// and what the public site does while it is on. The contract is the 「維護模式」
// bullet, the 選項鍵 maintenance entries (maintenance_retry_after defaults to 600)
// and the 錯誤與邊界 limit of 280 characters for the message. The front-end
// behaviour belongs to ticket 05; the status code and Retry-After rule here follow
// it. These tests fail until 08b and 05 land. Do not edit them to match an
// implementation.
//
// Assumptions: PUT /api/admin/maintenance answers 200 and GET returns enabled,
// message, retry_after and elapsed_minutes. The message limit counts characters.
// While maintenance is on, public pages and robots.txt answer 503 with
// Retry-After, and the admin area stays reachable.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func TestMaintenanceAPIToggleAndMessage(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-maint-admin")
	token := h.token(admin)

	on := h.request(http.MethodPut, "/api/admin/maintenance", s08bJSON(t, map[string]any{"enabled": true, "message": "Back soon", "retry_after": 600}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(on, "PUT /api/admin/maintenance on")
	s08bWant(t, "PUT /api/admin/maintenance enabled", on, http.StatusOK)

	started := s08bOptionValue(t, h, "maintenance_started_at")
	if _, err := time.Parse(time.RFC3339, started); err != nil {
		t.Errorf("maintenance_started_at = %q, want an RFC 3339 time set when maintenance turns on", started)
	}
	if got := s08bOptionValue(t, h, "maintenance_retry_after"); got != "600" {
		t.Errorf("maintenance_retry_after = %q, want 600", got)
	}

	get := h.request(http.MethodGet, "/api/admin/maintenance", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(get, "GET /api/admin/maintenance")
	s08bWant(t, "GET /api/admin/maintenance", get, http.StatusOK)
	decoded := s08bDecode(t, get.body)
	if enabled, _ := s08bFindKey(decoded, "enabled"); enabled != true {
		t.Errorf("GET maintenance enabled = %v, want true", enabled)
	}
	if retry, _ := s08bFindKey(decoded, "retry_after"); retry != float64(600) {
		t.Errorf("GET maintenance retry_after = %v, want 600", retry)
	}
	if msg, _ := s08bFindKey(decoded, "message"); msg != "Back soon" {
		t.Errorf("GET maintenance message = %v, want Back soon", msg)
	}
	elapsed, ok := s08bFindKey(decoded, "elapsed_minutes")
	if !ok {
		t.Errorf("GET maintenance has no elapsed_minutes; body=%q", s08bSnippet(get.body))
	} else if n, isNum := s08bNumber(elapsed); !isNum || n < 0 {
		t.Errorf("elapsed_minutes = %v, want a non-negative number", elapsed)
	}

	tooLong := h.request(http.MethodPut, "/api/admin/maintenance", s08bJSON(t, map[string]any{"enabled": true, "message": strings.Repeat("m", 281)}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(tooLong, "PUT maintenance with 281-character message")
	if tooLong.code != http.StatusBadRequest {
		t.Errorf("281-character message returned %d, want 400", tooLong.code)
	}
	atLimit := h.request(http.MethodPut, "/api/admin/maintenance", s08bJSON(t, map[string]any{"enabled": true, "message": strings.Repeat("m", 280)}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(atLimit, "PUT maintenance with 280-character message")
	s08bWant(t, "PUT maintenance with a 280-character message", atLimit, http.StatusOK)

	off := h.request(http.MethodPut, "/api/admin/maintenance", s08bJSON(t, map[string]any{"enabled": false}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(off, "PUT /api/admin/maintenance off")
	s08bWant(t, "PUT /api/admin/maintenance disabled", off, http.StatusOK)
}

func TestMaintenanceAPIAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-maint-only-admin")
	editor := h.user("editor", "s08b-maint-editor")
	author := h.user("author", "s08b-maint-author")
	adminToken, editorToken, authorToken := h.token(admin), h.token(editor), h.token(author)

	body := s08bJSON(t, map[string]any{"enabled": true, "message": "Editors cannot switch this"})
	for name, token := range map[string]string{"editor": editorToken, "author": authorToken} {
		get := h.request(http.MethodGet, "/api/admin/maintenance", "", token, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(get, "GET maintenance as "+name)
		if get.code != http.StatusForbidden {
			t.Errorf("GET /api/admin/maintenance as %s returned %d, want 403", name, get.code)
		}
		put := h.request(http.MethodPut, "/api/admin/maintenance", body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(put, "PUT maintenance as "+name)
		if put.code != http.StatusForbidden {
			t.Errorf("PUT /api/admin/maintenance as %s returned %d, want 403", name, put.code)
		}
	}
	if got := s08bOptionValue(t, h, "maintenance_enabled"); got == "true" {
		t.Errorf("an editor or author turned maintenance on")
	}

	// Positive control: the admin reads the state.
	ok := h.request(http.MethodGet, "/api/admin/maintenance", "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(ok, "GET maintenance as admin")
	s08bWant(t, "GET /api/admin/maintenance as admin", ok, http.StatusOK)
}

func TestMaintenanceRobotsAndStatus503(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-maint-503-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-maint-503-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Maint Probe Post", slug: "maint-probe"})

	// Turn maintenance on through the options, so the test does not depend on the PUT endpoint.
	s08bSetOptions(t, h, token, map[string]string{"maintenance_enabled": "true", "maintenance_retry_after": "600"})

	for _, path := range []string{"/", "/post/maint-probe", "/robots.txt"} {
		r := s08bGet(h, path, nil)
		s08bWant(t, "GET "+path+" during maintenance", r, http.StatusServiceUnavailable)
		if got := r.header.Get("Retry-After"); got != "600" {
			t.Errorf("GET %s Retry-After = %q, want 600", path, got)
		}
	}

	admin503 := h.request(http.MethodGet, "/api/admin/maintenance", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(admin503, "GET /api/admin/maintenance during maintenance")
	s08bWant(t, "GET /api/admin/maintenance as admin during maintenance", admin503, http.StatusOK)

	panel := h.request(http.MethodGet, "/admin/settings/site", "", token, echo.MIMETextHTML, "")
	h.mustNoPanic(panel, "GET /admin/settings/site during maintenance")
	s08bWant(t, "GET /admin/settings/site as admin during maintenance", panel, http.StatusOK)
}

func TestMaintenanceNoDBHitPerRequestWhenOff(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-maint-db-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-maint-db-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Maint Quiet Post", slug: "maint-quiet"})
	s08bSetOptions(t, h, token, map[string]string{"maintenance_enabled": "false"})

	// Count the queries that read the maintenance keys while maintenance is off.
	maintenanceQueries := 0
	probe := func(tx *gorm.DB) {
		if strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "maintenance") {
			maintenanceQueries++
		}
	}
	const name = "s08b:maintenance-probe"
	if err := h.db.Callback().Query().After("gorm:query").Register(name, probe); err != nil {
		t.Fatalf("register query probe: %v", err)
	}
	if err := h.db.Callback().Row().After("gorm:row").Register(name+":row", probe); err != nil {
		t.Fatalf("register row probe: %v", err)
	}
	t.Cleanup(func() {
		_ = h.db.Callback().Query().Remove(name)
		_ = h.db.Callback().Row().Remove(name + ":row")
	})

	// Warm up once, then count across several public requests.
	s08bGet(h, "/", nil)
	maintenanceQueries = 0
	for i := 0; i < 5; i++ {
		s08bWant(t, fmt.Sprintf("GET / request %d", i), s08bGet(h, "/", nil), http.StatusOK)
		s08bWant(t, fmt.Sprintf("GET /post/maint-quiet request %d", i), s08bGet(h, "/post/maint-quiet", nil), http.StatusOK)
	}
	if maintenanceQueries != 0 {
		t.Errorf("10 public requests with maintenance off ran %d queries on the maintenance keys, want 0", maintenanceQueries)
	}
}
