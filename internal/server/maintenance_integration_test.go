package server

// Maintenance-mode integration tests for ticket 05 slot B3 (public theme). The contract is the 契約 section
// of docs/tickets/05-public-theme.md and the header comment of maintenance.go. They are written before the
// gate is implemented and are expected to fail: public pages still answer 200 while maintenance is on.
// Do not edit them to match an implementation. Fixtures are seeded directly into the isolated PostgreSQL
// database; the behavior under test always goes through HTTP. Run with JELLY_TEST_POSTGRES=1, otherwise the
// harness skips.
//
// Settings are written as option rows: maintenance_enabled ("true"/"false"), maintenance_message and
// maintenance_started_at (RFC3339). The admin settings API saves options with POST /api/admin/settings
// (handlers_admin.go, used by web/admin/settings.html); the ticket text says PUT, but the route in code is POST.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// maintenanceTestMessage is the visitor-facing message used by the anonymous and API cases. It is ASCII so
// that a rendering problem cannot hide a failed message check behind encoding differences.
const maintenanceTestMessage = "Database upgrade in progress MNT-42"

// maintenanceSetOption replaces one option row, as the settings screen would store it.
func maintenanceSetOption(t *testing.T, h *accessHarness, key, value string) {
	t.Helper()
	if err := h.db.Unscoped().Where("key = ?", key).Delete(&core.Option{}).Error; err != nil {
		t.Fatalf("clear option %s: %v", key, err)
	}
	if err := h.db.Create(&core.Option{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("write option %s: %v", key, err)
	}
}

// maintenanceOption reads one option row. found is false when the row does not exist.
func maintenanceOption(t *testing.T, h *accessHarness, key string) (value string, found bool) {
	t.Helper()
	var opt core.Option
	err := h.db.Where("key = ?", key).First(&opt).Error
	if err != nil {
		if strings.Contains(err.Error(), "record not found") {
			return "", false
		}
		t.Fatalf("read option %s: %v", key, err)
	}
	return opt.Value, true
}

// maintenanceEnable turns maintenance on with the test message and backdates the start time by minutesAgo.
func maintenanceEnable(t *testing.T, h *accessHarness, minutesAgo int) {
	t.Helper()
	if err := setMaintenance(h.db, true, maintenanceTestMessage); err != nil {
		t.Fatalf("setMaintenance(true): %v", err)
	}
	if minutesAgo > 0 {
		started := time.Now().UTC().Add(-time.Duration(minutesAgo) * time.Minute)
		maintenanceSetOption(t, h, "maintenance_started_at", started.Format(time.RFC3339))
	}
}

// maintenanceTagPattern matches markup; removing it so that a figure split across tags is still found in the text.
var maintenanceTagPattern = regexp.MustCompile(`<[^>]*>`)

// maintenanceElapsedPattern matches the elapsed-minutes figure "4 分" (with 分鐘 allowed after it) as a
// standalone number, not part of a longer one.
var maintenanceElapsedPattern = regexp.MustCompile(`(^|[^0-9])4\s*分`)

func maintenanceVisitorBody(body string) string {
	return maintenanceTagPattern.ReplaceAllString(body, " ")
}

func TestMaintenanceOffServesNormally(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "mnt-off-author")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Maintenance Off Visible Post",
		publishedAt: archivesDay(2025, time.March, 3)})
	maintenanceSetOption(t, h, "maintenance_enabled", "false")

	r := archivesGet(t, h, "/")
	if r.code != http.StatusOK {
		t.Fatalf("GET / with maintenance off: status %d, want 200", r.code)
	}
	if !strings.Contains(r.body, post.Title) {
		t.Errorf("GET / with maintenance off does not show the seeded post %q", post.Title)
	}
	if r := archivesGet(t, h, "/post/"+post.Slug); r.code != http.StatusOK {
		t.Errorf("GET /post/%s with maintenance off: status %d, want 200", post.Slug, r.code)
	}
}

func TestMaintenanceOnAnonymousGets503WithRetryAfter(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "mnt-anon-author")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Maintenance Hidden Post",
		publishedAt: archivesDay(2025, time.March, 4)})
	maintenanceEnable(t, h, 4)

	r := archivesGet(t, h, "/")
	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("anonymous GET / during maintenance: status %d, want 503", r.code)
	}
	if got := r.header.Get("Retry-After"); got != "600" {
		t.Errorf("Retry-After = %q, want %q", got, "600")
	}
	for _, want := range []string{maintenanceTestMessage, "維護中", "重新整理", "管理員登入"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("maintenance page does not contain %q", want)
		}
	}
	if !maintenanceElapsedPattern.MatchString(maintenanceVisitorBody(r.body)) {
		t.Errorf("maintenance page does not show the elapsed minutes (4) computed from maintenance_started_at")
	}
	if strings.Contains(r.body, post.Title) {
		t.Errorf("maintenance page leaks the seeded post title %q", post.Title)
	}
}

func TestMaintenanceOnAdminAndEditorSeeSite(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "mnt-role-author")
	editor := h.user("editor", "mnt-role-editor")
	admin := h.user("admin", "mnt-role-admin")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Maintenance Visible Post",
		publishedAt: archivesDay(2025, time.March, 5)})
	maintenanceEnable(t, h, 0)

	for _, tc := range []struct {
		name  string
		user  core.User
		want  int
		title bool
	}{
		{"admin", admin, http.StatusOK, true},
		{"editor", editor, http.StatusOK, true},
		{"author", author, http.StatusServiceUnavailable, false},
	} {
		r := h.request(http.MethodGet, "/", "", h.token(tc.user), echo.MIMETextHTML, "")
		if r.panicked != nil {
			t.Fatalf("%s GET / panicked: %v", tc.name, r.panicked)
		}
		if r.code != tc.want {
			t.Errorf("%s GET / during maintenance: status %d, want %d", tc.name, r.code, tc.want)
			continue
		}
		if has := strings.Contains(r.body, post.Title); has != tc.title {
			t.Errorf("%s GET / during maintenance: contains post title = %v, want %v", tc.name, has, tc.title)
		}
	}
}

func TestMaintenanceOnKeepsLoginAdminAssetsAndInstallReachable(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "mnt-keep-admin")
	maintenanceEnable(t, h, 0)

	if r := archivesGet(t, h, "/login"); r.code != http.StatusOK {
		t.Errorf("GET /login during maintenance: status %d, want 200", r.code)
	}
	if r := h.request(http.MethodGet, "/admin", "", h.token(admin), echo.MIMETextHTML, ""); r.code == http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("GET /admin for signed-in admin during maintenance: status %d panicked=%v, want not 503", r.code, r.panicked)
	}
	if r := h.request(http.MethodGet, "/api/admin/posts", "", h.token(admin), echo.MIMEApplicationJSON, ""); r.code == http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("GET /api/admin/posts for admin during maintenance: status %d panicked=%v, want not 503", r.code, r.panicked)
	}
	if r := h.request(http.MethodPost, "/auth/login", `{"username":"nobody","password":"wrong"}`, "", echo.MIMEApplicationJSON, echo.MIMEApplicationJSON); r.code == http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("POST /auth/login during maintenance: status %d panicked=%v, want not 503", r.code, r.panicked)
	}
	if r := archivesGet(t, h, "/uploads/x.png"); r.code == http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("GET /uploads/x.png during maintenance: status %d panicked=%v, want not 503", r.code, r.panicked)
	}
	if r := archivesGet(t, h, "/install"); r.code == http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("GET /install during maintenance: status %d panicked=%v, want not 503", r.code, r.panicked)
	}
}

func TestMaintenanceOnAPIReturnsJSON503(t *testing.T) {
	h := archivesHarness(t)
	maintenanceEnable(t, h, 0)

	// Ask for HTML on purpose: /api/* must answer JSON whatever the client accepts.
	r := h.request(http.MethodGet, "/api/comments?post_id=1", "", "", echo.MIMETextHTML, "")
	if r.panicked != nil {
		t.Fatalf("anonymous GET /api/comments panicked: %v", r.panicked)
	}
	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("anonymous GET /api/comments during maintenance: status %d, want 503", r.code)
	}
	if ct := r.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatalf("503 body is not JSON: %v (body %q)", err, r.body)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("503 JSON body has no \"error\" key: %q", r.body)
	}
}

func TestMaintenanceSetAndClearRecordsStartTime(t *testing.T) {
	h := archivesHarness(t)

	if err := setMaintenance(h.db, true, maintenanceTestMessage); err != nil {
		t.Fatalf("setMaintenance(true): %v", err)
	}
	if enabled, _ := maintenanceOption(t, h, "maintenance_enabled"); enabled != "true" {
		t.Errorf("maintenance_enabled = %q after enable, want %q", enabled, "true")
	}
	if msg, _ := maintenanceOption(t, h, "maintenance_message"); msg != maintenanceTestMessage {
		t.Errorf("maintenance_message = %q after enable, want %q", msg, maintenanceTestMessage)
	}
	first, ok := maintenanceOption(t, h, "maintenance_started_at")
	if !ok {
		t.Fatal("maintenance_started_at was not written when maintenance was enabled")
	}
	started, err := time.Parse(time.RFC3339, first)
	if err != nil {
		t.Fatalf("maintenance_started_at %q is not RFC3339: %v", first, err)
	}
	if d := time.Since(started); d < -5*time.Second || d > 5*time.Second {
		t.Errorf("maintenance_started_at %s is not within 5s of now", first)
	}

	// A second enable call keeps the original start time. Backdate it first so that a rewrite is visible
	// even within the same second.
	backdated := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	maintenanceSetOption(t, h, "maintenance_started_at", backdated.Format(time.RFC3339))
	if err := setMaintenance(h.db, true, "second call"); err != nil {
		t.Fatalf("setMaintenance(true) second call: %v", err)
	}
	if kept, _ := maintenanceOption(t, h, "maintenance_started_at"); kept != backdated.Format(time.RFC3339) {
		t.Errorf("second enable changed maintenance_started_at to %q, want kept %q", kept, backdated.Format(time.RFC3339))
	}

	if err := setMaintenance(h.db, false, ""); err != nil {
		t.Fatalf("setMaintenance(false): %v", err)
	}
	if enabled, _ := maintenanceOption(t, h, "maintenance_enabled"); enabled != "false" {
		t.Errorf("maintenance_enabled = %q after disable, want %q", enabled, "false")
	}
	if cleared, ok := maintenanceOption(t, h, "maintenance_started_at"); ok && cleared != "" {
		t.Errorf("maintenance_started_at = %q after disable, want it cleared", cleared)
	}
}

func TestMaintenanceSettingsAPIWritesOptions(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "mnt-api-admin")
	post := archivesSeedPost(t, h, admin, archivesPost{title: "Maintenance API Post",
		publishedAt: archivesDay(2025, time.March, 6)})
	started := time.Now().UTC().Add(-4 * time.Minute).Truncate(time.Second)
	payload, err := json.Marshal(map[string]string{
		"maintenance_enabled":    "true",
		"maintenance_message":    maintenanceTestMessage,
		"maintenance_started_at": started.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal settings payload: %v", err)
	}

	r := h.request(http.MethodPost, "/api/admin/settings", string(payload), h.token(admin), echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if r.panicked != nil {
		t.Fatalf("admin POST /api/admin/settings panicked: %v", r.panicked)
	}
	if r.code != http.StatusOK {
		t.Fatalf("admin POST /api/admin/settings with maintenance keys: status %d, want 200 (body %q)", r.code, r.body)
	}
	if v, ok := maintenanceOption(t, h, "maintenance_enabled"); !ok || v != "true" {
		t.Errorf("maintenance_enabled row = %q (found %v), want %q", v, ok, "true")
	}
	if v, ok := maintenanceOption(t, h, "maintenance_message"); !ok || v != maintenanceTestMessage {
		t.Errorf("maintenance_message row = %q (found %v), want %q", v, ok, maintenanceTestMessage)
	}
	if v, ok := maintenanceOption(t, h, "maintenance_started_at"); !ok || v != started.Format(time.RFC3339) {
		t.Errorf("maintenance_started_at row = %q (found %v), want %q", v, ok, started.Format(time.RFC3339))
	}

	// The saved options must take effect: an anonymous visitor now gets the maintenance page.
	if r := archivesGet(t, h, "/"); r.code != http.StatusServiceUnavailable {
		t.Errorf("anonymous GET / after saving maintenance_enabled=true through the API: status %d, want 503", r.code)
	} else if strings.Contains(r.body, post.Title) {
		t.Errorf("maintenance page leaks the seeded post title %q", post.Title)
	}
}
