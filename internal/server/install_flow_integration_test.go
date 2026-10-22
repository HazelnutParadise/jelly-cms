package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"gorm.io/gorm"
)

const (
	installFlowHost          = "127.0.0.1"
	installFlowPort          = "55432"
	installFlowUser          = "jelly_test"
	installFlowPassword      = "jelly-local-acceptance"
	installFlowAdminDatabase = "jelly_acceptance"
	installFlowDatabasePfx   = "jelly_flow_"

	installFlowSiteTitle  = "Install Flow Site"
	installFlowAdminUser  = "flowadmin"
	installFlowAdminEmail = "flowadmin@example.com"
	installFlowAdminPass  = "InstallFlowPass123!"

	installFlowSecondUser  = "flowintruder"
	installFlowSecondEmail = "flowintruder@example.com"
	installFlowSecondPass  = "AnotherInstallPass123!"
)

var installFlowDatabaseCounter atomic.Int64

// requireInstallFlowPostgres keeps these end-to-end tests opt-in so a normal
// go test run never depends on a running PostgreSQL server.
func requireInstallFlowPostgres(t *testing.T) {
	t.Helper()

	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run the HTTP installation flow against the dedicated PostgreSQL container")
	}
}

func installFlowDatabaseConfig(name string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     installFlowHost,
		Port:     installFlowPort,
		User:     installFlowUser,
		Password: installFlowPassword,
		Name:     name,
		Timezone: "UTC",
	}
}

func closeInstallFlowHandle(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return
	}
	_ = sqlDB.Close()
}

func openInstallFlowHandle(t *testing.T, name string) *gorm.DB {
	t.Helper()

	db, err := data.Open(installFlowDatabaseConfig(name))
	if err != nil {
		t.Fatalf("open PostgreSQL handle for %s/%s: %v", installFlowHost, name, err)
	}
	t.Cleanup(func() { closeInstallFlowHandle(db) })
	return db
}

// createInstallFlowDatabase creates a database that belongs only to this test.
// The cleanup refuses names outside the installFlowDatabasePfx namespace, closes
// every connection to that database, and drops only the created database.
func createInstallFlowDatabase(t *testing.T, admin *gorm.DB) string {
	t.Helper()

	name := fmt.Sprintf("%s%d_%d_%d", installFlowDatabasePfx, os.Getpid(), time.Now().UnixNano(), installFlowDatabaseCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)).Error; err != nil {
		t.Fatalf("create the database owned by this test %s: %v", name, err)
	}

	var serverVersion int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&serverVersion).Error; err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}

	t.Cleanup(func() {
		if !strings.HasPrefix(name, installFlowDatabasePfx) {
			t.Errorf("refusing to drop database %q because it is outside the %s namespace", name, installFlowDatabasePfx)
			return
		}

		_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, name)).Error
		_ = admin.Exec(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()",
			name,
		).Error

		drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, name)
		if serverVersion >= 130000 {
			drop += " WITH (FORCE)"
		}
		if err := admin.Exec(drop).Error; err != nil {
			t.Errorf("drop the database owned by this test %s: %v", name, err)
		}
	})

	return name
}

// installFlowHarness is one uninstalled server instance backed by one fresh
// database. It intentionally does not use t.Parallel: it swaps the package
// globals and changes the working directory like the real server does.
type installFlowHarness struct {
	t        *testing.T
	server   *echo.Echo
	cookies  map[string]string
	database string
}

type installFlowResult struct {
	code     int
	body     string
	panicked any
}

func newInstallFlowHarness(t *testing.T) *installFlowHarness {
	t.Helper()
	requireInstallFlowPostgres(t)

	admin := openInstallFlowHandle(t, installFlowAdminDatabase)
	database := createInstallFlowDatabase(t, admin)

	root := repoRoot(t)
	work := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "web"), filepath.Join(work, "web")); err != nil {
		t.Fatalf("link the web assets into the temporary working directory: %v", err)
	}
	t.Chdir(work)

	previousBundle := i18n.Bundle
	t.Cleanup(func() { i18n.Bundle = previousBundle })

	i18n.Init()
	if i18n.Bundle == nil {
		t.Fatal("i18n.Init() left the bundle nil")
	}
	if _, err := i18n.GetLocalizer("zh-TW").Localize(&goi18n.LocalizeConfig{MessageID: "welcome"}); err != nil {
		t.Fatalf("the zh-TW locale was not loaded: %v", err)
	}

	previousConfig := config.GlobalConfig
	previousDB := data.DB
	config.GlobalConfig = nil
	data.DB = nil
	t.Cleanup(func() {
		current := data.DB
		config.GlobalConfig = previousConfig
		data.DB = previousDB
		if current != nil && current != previousDB {
			closeInstallFlowHandle(current)
		}
	})

	if config.GlobalConfig != nil || data.DB != nil || install.IsInstalled() {
		t.Fatalf("the harness must start uninstalled with a nil database handle; config=%+v db=%p installed=%v",
			config.GlobalConfig, data.DB, install.IsInstalled())
	}

	// Match cmd/server/main.go: i18n and auth are initialized at startup and the
	// bundled theme is activated before the routes are registered.
	auth.Init()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate the bundled default theme: %v", err)
	}

	// This is the behavior under test: RegisterRoutes must run while the site is
	// uninstalled and there is no live database handle.
	e := echo.New()
	RegisterRoutes(e, tm, nil)

	if config.GlobalConfig != nil || data.DB != nil || install.IsInstalled() {
		t.Fatalf("RegisterRoutes changed the uninstalled state; config=%+v db=%p installed=%v",
			config.GlobalConfig, data.DB, install.IsInstalled())
	}

	return &installFlowHarness{
		t:        t,
		server:   e,
		cookies:  make(map[string]string),
		database: database,
	}
}

// do sends one request through the same Echo instance and carries the cookies
// set by earlier responses. A panic is captured instead of aborting the test so
// each flow test reports its own nil-captured-service failure.
func (h *installFlowHarness) do(method, target, body string) installFlowResult {
	h.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	for name, value := range h.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}

	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.server.ServeHTTP(rec, req)
	}()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge < 0 || cookie.Value == "" {
			delete(h.cookies, cookie.Name)
			continue
		}
		h.cookies[cookie.Name] = cookie.Value
	}

	return installFlowResult{code: rec.Code, body: rec.Body.String(), panicked: panicked}
}

func (h *installFlowHarness) requireOK(method, target, body string) installFlowResult {
	h.t.Helper()

	result := h.do(method, target, body)
	if result.panicked != nil {
		h.t.Fatalf("%s %s panicked after installation: %v", method, target, result.panicked)
	}
	if result.code != http.StatusOK {
		h.t.Fatalf("%s %s returned %d, want 200; body=%q", method, target, result.code, result.body)
	}
	return result
}

func (h *installFlowHarness) installBody() string {
	h.t.Helper()

	raw, err := json.Marshal(map[string]string{
		"db_host":     installFlowHost,
		"db_port":     installFlowPort,
		"db_user":     installFlowUser,
		"db_pass":     installFlowPassword,
		"db_name":     h.database,
		"db_timezone": "UTC",
		"site_title":  installFlowSiteTitle,
		"admin_user":  installFlowAdminUser,
		"admin_email": installFlowAdminEmail,
		"admin_pass":  installFlowAdminPass,
	})
	if err != nil {
		h.t.Fatalf("encode the installation payload: %v", err)
	}
	return string(raw)
}

func (h *installFlowHarness) expectInstallerOpen() {
	h.t.Helper()

	result := h.do(http.MethodGet, "/install", "")
	if result.panicked != nil {
		h.t.Fatalf("GET /install panicked before installation: %v", result.panicked)
	}
	if result.code != http.StatusOK {
		h.t.Fatalf("GET /install returned %d before installation, want 200; body=%q", result.code, result.body)
	}
	if !strings.Contains(result.body, "Install Jelly CMS") {
		h.t.Fatalf("GET /install did not serve the installation form before installation; body=%q", result.body)
	}
}

func (h *installFlowHarness) installSite() {
	h.t.Helper()

	result := h.do(http.MethodPost, "/api/install", h.installBody())
	if result.panicked != nil {
		h.t.Fatalf("POST /api/install panicked: %v", result.panicked)
	}
	if result.code != http.StatusOK {
		h.t.Fatalf("POST /api/install returned %d, want 200; body=%q", result.code, result.body)
	}

	if config.GlobalConfig == nil {
		h.t.Fatal("config.GlobalConfig = nil after installation, want the installed config immediately")
	}
	if got := config.GlobalConfig.Database.Name; got != h.database {
		h.t.Fatalf("config.GlobalConfig.Database.Name = %q, want %q", got, h.database)
	}
	if data.DB == nil {
		h.t.Fatal("data.DB = nil after installation, want a live handle without a restart")
	}
	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		h.t.Fatalf("query the database behind data.DB after installation: %v", err)
	}
	if current != h.database {
		h.t.Fatalf("data.DB is connected to %q, want %q", current, h.database)
	}
	if !install.IsInstalled() {
		h.t.Fatal("install.IsInstalled() = false after a successful installation")
	}
}

// loginSite authenticates through the real local-login endpoint used by
// web/admin/login.html and keeps the cookies for the following requests.
func (h *installFlowHarness) loginSite() {
	h.t.Helper()

	raw, err := json.Marshal(map[string]string{
		"username": installFlowAdminUser,
		"password": installFlowAdminPass,
	})
	if err != nil {
		h.t.Fatalf("encode the login payload: %v", err)
	}

	result := h.do(http.MethodPost, "/auth/login", string(raw))
	if result.panicked != nil {
		h.t.Fatalf("POST /auth/login panicked after installation: %v", result.panicked)
	}
	if result.code != http.StatusOK {
		h.t.Fatalf("POST /auth/login returned %d, want 200; body=%q", result.code, result.body)
	}
	if _, ok := h.cookies["jwt_token"]; !ok {
		h.t.Fatalf("POST /auth/login did not set the jwt_token cookie; cookies=%v", h.cookies)
	}

	me := h.requireOK(http.MethodGet, "/api/auth/me", "")
	var user struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal([]byte(me.body), &user); err != nil {
		h.t.Fatalf("decode /api/auth/me response %q: %v", me.body, err)
	}
	if user.Username != installFlowAdminUser {
		h.t.Fatalf("authenticated user = %q, want the newly installed admin %q", user.Username, installFlowAdminUser)
	}
	if user.Role != "admin" {
		h.t.Fatalf("authenticated role = %q, want admin", user.Role)
	}
}

// TestInstallFlowPostCrudWithoutRestart installs through HTTP, logs in, and
// exercises the post API on the same server instance. A nil handle captured by
// postService at registration shows up as a panic or 500 in this test only.
func TestInstallFlowPostCrudWithoutRestart(t *testing.T) {
	h := newInstallFlowHarness(t)
	h.expectInstallerOpen()
	h.installSite()
	h.loginSite()

	created := h.requireOK(http.MethodPost, "/api/admin/posts",
		`{"title":"Install Flow Post","content":"<p>Created after installation.</p>","type":"post","status":"published"}`)
	var post core.Post
	if err := json.Unmarshal([]byte(created.body), &post); err != nil {
		t.Fatalf("decode the created post %q: %v", created.body, err)
	}
	if post.ID == 0 {
		t.Fatalf("created post id = 0, want a persisted row; body=%q", created.body)
	}
	if post.Title != "Install Flow Post" {
		t.Fatalf("created post title = %q, want %q", post.Title, "Install Flow Post")
	}
	if post.Slug != "install-flow-post" {
		t.Fatalf("created post slug = %q, want %q", post.Slug, "install-flow-post")
	}

	got := h.requireOK(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", post.ID), "")
	var fetched core.Post
	if err := json.Unmarshal([]byte(got.body), &fetched); err != nil {
		t.Fatalf("decode the fetched post %q: %v", got.body, err)
	}
	if fetched.ID != post.ID || fetched.Title != post.Title {
		t.Fatalf("GET post = %+v, want the created post %+v", fetched, post)
	}

	updated := h.requireOK(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", post.ID),
		`{"title":"Updated Install Flow Post","content":"<p>Updated after installation.</p>","type":"post","status":"published"}`)
	var saved core.Post
	if err := json.Unmarshal([]byte(updated.body), &saved); err != nil {
		t.Fatalf("decode the updated post %q: %v", updated.body, err)
	}
	if saved.ID != post.ID || saved.Title != "Updated Install Flow Post" {
		t.Fatalf("updated post = %+v, want id=%d title=%q", saved, post.ID, "Updated Install Flow Post")
	}

	h.requireOK(http.MethodDelete, fmt.Sprintf("/api/admin/posts/%d", post.ID), "")

	after := h.do(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", post.ID), "")
	if after.panicked != nil {
		t.Fatalf("GET a deleted post panicked: %v", after.panicked)
	}
	if after.code != http.StatusNotFound {
		t.Fatalf("GET a deleted post returned %d, want 404; body=%q", after.code, after.body)
	}
}

// TestInstallFlowProductCrudWithoutRestart keeps the product flow separate from
// the post flow so a post panic cannot hide a nil handle captured by
// productService.
func TestInstallFlowProductCrudWithoutRestart(t *testing.T) {
	h := newInstallFlowHarness(t)
	h.expectInstallerOpen()
	h.installSite()
	h.loginSite()

	created := h.requireOK(http.MethodPost, "/api/admin/products",
		`{"title":"Install Flow Product","description":"Created after installation.","price":1999,"sku":"FLOW-1","stock":7,"status":"active"}`)
	var product core.Product
	if err := json.Unmarshal([]byte(created.body), &product); err != nil {
		t.Fatalf("decode the created product %q: %v", created.body, err)
	}
	if product.ID == 0 {
		t.Fatalf("created product id = 0, want a persisted row; body=%q", created.body)
	}
	if product.Title != "Install Flow Product" {
		t.Fatalf("created product title = %q, want %q", product.Title, "Install Flow Product")
	}
	if product.Slug != "install-flow-product" {
		t.Fatalf("created product slug = %q, want %q", product.Slug, "install-flow-product")
	}
	if product.Price != 1999 || product.Stock != 7 {
		t.Fatalf("created product price/stock = %d/%d, want 1999/7", product.Price, product.Stock)
	}

	got := h.requireOK(http.MethodGet, fmt.Sprintf("/api/admin/products/%d", product.ID), "")
	var fetched core.Product
	if err := json.Unmarshal([]byte(got.body), &fetched); err != nil {
		t.Fatalf("decode the fetched product %q: %v", got.body, err)
	}
	if fetched.ID != product.ID || fetched.Title != product.Title {
		t.Fatalf("GET product = %+v, want the created product %+v", fetched, product)
	}

	updated := h.requireOK(http.MethodPut, fmt.Sprintf("/api/admin/products/%d", product.ID),
		`{"title":"Updated Install Flow Product","description":"Updated after installation.","price":2499,"sku":"FLOW-1","stock":3,"status":"active"}`)
	var saved core.Product
	if err := json.Unmarshal([]byte(updated.body), &saved); err != nil {
		t.Fatalf("decode the updated product %q: %v", updated.body, err)
	}
	if saved.ID != product.ID || saved.Title != "Updated Install Flow Product" || saved.Price != 2499 || saved.Stock != 3 {
		t.Fatalf("updated product = %+v, want id=%d title=%q price=2499 stock=3", saved, product.ID, "Updated Install Flow Product")
	}

	h.requireOK(http.MethodDelete, fmt.Sprintf("/api/admin/products/%d", product.ID), "")

	after := h.do(http.MethodGet, fmt.Sprintf("/api/admin/products/%d", product.ID), "")
	if after.panicked != nil {
		t.Fatalf("GET a deleted product panicked: %v", after.panicked)
	}
	if after.code != http.StatusNotFound {
		t.Fatalf("GET a deleted product returned %d, want 404; body=%q", after.code, after.body)
	}
}

// TestInstallFlowPublicHomeWithoutRestart proves the public home page reads the
// freshly installed database through the same server instance. The post is
// created through the admin API so the whole flow is HTTP-only.
func TestInstallFlowPublicHomeWithoutRestart(t *testing.T) {
	h := newInstallFlowHarness(t)
	h.expectInstallerOpen()
	h.installSite()
	h.loginSite()

	h.requireOK(http.MethodPost, "/api/admin/posts",
		`{"title":"Install Flow Home Post","content":"<p>Visible on the home page without a restart.</p>","type":"post","status":"published"}`)

	home := h.requireOK(http.MethodGet, "/", "")
	if !strings.Contains(home.body, installFlowSiteTitle) {
		t.Fatalf("home page does not show the installed site title %q; body=%q", installFlowSiteTitle, home.body)
	}
	if !strings.Contains(home.body, "Install Flow Home Post") {
		t.Fatalf("home page does not show the post published after installation; body=%q", home.body)
	}
}

// TestInstallFlowRepeatedInstallationRejected verifies that a second install
// request on the same server keeps the first site intact and answers 409.
func TestInstallFlowRepeatedInstallationRejected(t *testing.T) {
	h := newInstallFlowHarness(t)
	h.expectInstallerOpen()
	h.installSite()
	h.loginSite()

	var originalAdmins []core.User
	if err := data.DB.Where("role = ?", "admin").Order("id").Find(&originalAdmins).Error; err != nil {
		t.Fatalf("query the installed administrator: %v", err)
	}
	if len(originalAdmins) != 1 {
		t.Fatalf("admin count after installation = %d, want exactly 1", len(originalAdmins))
	}
	original := originalAdmins[0]

	second, err := json.Marshal(map[string]string{
		"db_host":     installFlowHost,
		"db_port":     installFlowPort,
		"db_user":     installFlowUser,
		"db_pass":     installFlowPassword,
		"db_name":     h.database,
		"db_timezone": "UTC",
		"site_title":  "Replaced Install Flow Site",
		"admin_user":  installFlowSecondUser,
		"admin_email": installFlowSecondEmail,
		"admin_pass":  installFlowSecondPass,
	})
	if err != nil {
		t.Fatalf("encode the second installation payload: %v", err)
	}

	result := h.do(http.MethodPost, "/api/install", string(second))
	if result.panicked != nil {
		t.Fatalf("a repeated POST /api/install panicked: %v", result.panicked)
	}
	if result.code != http.StatusConflict {
		t.Fatalf("a repeated POST /api/install returned %d, want 409; body=%q", result.code, result.body)
	}
	for _, secret := range []string{installFlowSecondPass, installFlowPassword, installFlowAdminPass, "host=", "dbname="} {
		if strings.Contains(result.body, secret) {
			t.Fatalf("the repeated installation response exposes %q: %q", secret, result.body)
		}
	}

	var admins []core.User
	if err := data.DB.Where("role = ?", "admin").Order("id").Find(&admins).Error; err != nil {
		t.Fatalf("query administrators after the repeated installation: %v", err)
	}
	if len(admins) != 1 {
		t.Fatalf("admin count after the repeated installation = %d, want 1", len(admins))
	}
	if admins[0].ID != original.ID || admins[0].Username != original.Username || admins[0].Email != original.Email {
		t.Fatalf("the repeated installation changed the administrator: got %+v, want the original %+v", admins[0], original)
	}

	var title core.Option
	if err := data.DB.Where("key = ?", "site_title").First(&title).Error; err != nil {
		t.Fatalf("query the site title after the repeated installation: %v", err)
	}
	if title.Value != installFlowSiteTitle {
		t.Fatalf("site_title after the repeated installation = %q, want the original %q", title.Value, installFlowSiteTitle)
	}
}
