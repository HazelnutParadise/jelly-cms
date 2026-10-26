package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	accessTestHost    = "127.0.0.1"
	accessTestPort    = "55432"
	accessTestUser    = "jelly_test"
	accessTestPass    = "jelly-local-acceptance"
	accessTestAdminDB = "jelly_acceptance"
	accessTestPrefix  = "jelly_access_"
)

var accessTestDBCounter atomic.Int64

type accessHarness struct {
	echo *echo.Echo
	db   *gorm.DB
	t    *testing.T
}

func newAccessHarness(t *testing.T) *accessHarness {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run access-control HTTP tests against the dedicated PostgreSQL container")
	}

	admin, err := data.Open(config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: accessTestAdminDB, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	t.Cleanup(func() { accessCloseDB(admin) })
	dbName := fmt.Sprintf("%s%d_%d", accessTestPrefix, os.Getpid(), accessTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated access-test database %s: %v", dbName, err)
	}
	var version int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&version).Error; err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, accessTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, dbName)).Error
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)
		if version >= 130000 {
			drop += " WITH (FORCE)"
		}
		if err := admin.Exec(drop).Error; err != nil {
			t.Errorf("drop isolated database %s: %v", dbName, err)
		}
	})

	previousDB, previousConfig, previousBundle := data.DB, config.GlobalConfig, i18n.Bundle
	db, err := data.Open(config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated access-test database: %v", err)
	}
	db.Logger = db.Logger.LogMode(logger.Silent)
	data.DB = db
	config.GlobalConfig = &config.Config{Database: config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: dbName, Timezone: "UTC"}}
	t.Cleanup(func() {
		data.DB, config.GlobalConfig, i18n.Bundle = previousDB, previousConfig, previousBundle
		accessCloseDB(db)
	})
	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	root := filepath.Join(repoRoot(t), "web")
	work := t.TempDir()
	if err := copyAccessTree(root, filepath.Join(work, "web")); err != nil {
		t.Fatalf("copy web assets into isolated work directory: %v", err)
	}
	t.Chdir(work)
	i18n.Init()
	if i18n.Bundle == nil {
		t.Fatal("i18n.Init() left bundle nil")
	}
	if _, err := i18n.GetLocalizer("zh-TW").Localize(&goi18n.LocalizeConfig{MessageID: "welcome"}); err != nil {
		t.Fatalf("load zh-TW locale: %v", err)
	}
	auth.Init()
	e := echo.New()
	RegisterRoutes(e, theme.NewManager("web/themes"), nil)
	return &accessHarness{echo: e, db: db, t: t}
}

func copyAccessTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0644)
	})
}

func accessCloseDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

func (h *accessHarness) user(role, name string) core.User {
	h.t.Helper()
	u := core.User{Username: name, Email: name + "@example.test", Role: role, Provider: "local"}
	if err := h.db.Create(&u).Error; err != nil {
		h.t.Fatalf("create %s user: %v", role, err)
	}
	return u
}

func (h *accessHarness) token(u core.User) string {
	h.t.Helper()
	token, err := auth.GenerateJWT(u.ID, u.Email, u.Role)
	if err != nil {
		h.t.Fatalf("generate JWT for %s: %v", u.Username, err)
	}
	return token
}

type accessResult struct {
	code     int
	body     string
	header   http.Header
	panicked any
}

func (h *accessHarness) request(method, path, body, token, accept, contentType string) accessResult {
	h.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return h.requestReader(method, path, r, token, accept, contentType)
}

func (h *accessHarness) requestReader(method, path string, r io.Reader, token, accept, contentType string) accessResult {
	h.t.Helper()
	req := httptest.NewRequest(method, path, r)
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		// This trusted in-process fixture supplies the request's source evidence.
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	if accept != "" {
		req.Header.Set(echo.HeaderAccept, accept)
	}
	if contentType != "" {
		req.Header.Set(echo.HeaderContentType, contentType)
	}
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); h.echo.ServeHTTP(rec, req) }()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

func accessDatabaseSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var users []core.User
	var posts []core.Post
	var options []core.Option
	var products []core.Product
	var orders []core.Order
	var items []core.OrderItem
	var themeSettings []core.ThemeSettings
	for _, query := range []struct {
		name  string
		value any
		order string
	}{
		{"users", &users, "id"}, {"posts", &posts, "id"}, {"options", &options, "key"}, {"products", &products, "id"},
		{"orders", &orders, "id"}, {"order items", &items, "id"}, {"theme settings", &themeSettings, "theme_name"},
	} {
		if err := db.Order(query.order).Find(query.value).Error; err != nil {
			t.Fatalf("snapshot %s: %v", query.name, err)
		}
	}
	raw, err := json.Marshal([]any{users, posts, options, products, orders, items, themeSettings})
	if err != nil {
		t.Fatalf("marshal database snapshot: %v", err)
	}
	return string(raw)
}

func accessStateSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var files []string
	for _, root := range []string{"data", "web/themes"} {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			files = append(files, path+":"+hex.EncodeToString(sum[:]))
			return nil
		}); err != nil {
			t.Fatalf("snapshot isolated files under %s: %v", root, err)
		}
	}
	sort.Strings(files)
	return accessDatabaseSnapshot(t, db) + "\n" + strings.Join(files, "\n")
}

func (h *accessHarness) mustNoPanic(r accessResult, where string) {
	h.t.Helper()
	if r.panicked != nil {
		h.t.Errorf("%s panicked: %v", where, r.panicked)
	}
}

func routePath(path string) string {
	path = strings.ReplaceAll(path, ":id", "987654321")
	path = strings.ReplaceAll(path, ":filename", "access-missing-file.png")
	path = strings.ReplaceAll(path, ":name", "default")
	path = strings.ReplaceAll(path, ":plugin", "missing-plugin")
	return path
}

func TestAccessAnonymousAdminRouteTableAlwaysReturnsJSON401(t *testing.T) {
	h := newAccessHarness(t)
	var routes []*echo.Route
	for _, route := range h.echo.Routes() {
		if strings.HasPrefix(route.Path, "/api/admin") && (route.Method == http.MethodGet || route.Method == http.MethodPost || route.Method == http.MethodPut || route.Method == http.MethodDelete) {
			routes = append(routes, route)
		}
	}
	if len(routes) == 0 {
		t.Fatal("RegisterRoutes exposed no /api/admin routes")
	}
	for _, route := range routes {
		for _, headers := range []struct{ accept, contentType string }{
			{"", ""}, {"text/html", ""}, {"text/html", "text/plain"},
		} {
			before := accessStateSnapshot(t, h.db)
			result := h.request(route.Method, routePath(route.Path), `{ "title":"probe", "content":"probe", "type":"post", "status":"draft", "price":1, "sku":"probe", "stock":1 }`, "", headers.accept, headers.contentType)
			h.mustNoPanic(result, route.Method+" "+route.Path)
			if result.code != http.StatusUnauthorized {
				t.Errorf("anonymous %s %s Accept=%q Content-Type=%q returned %d, want 401; body=%q", route.Method, route.Path, headers.accept, headers.contentType, result.code, result.body)
			}
			if !strings.Contains(strings.ToLower(result.header.Get(echo.HeaderContentType)), "application/json") {
				t.Errorf("anonymous %s %s Accept=%q Content-Type=%q response content type=%q, want JSON", route.Method, route.Path, headers.accept, headers.contentType, result.header.Get(echo.HeaderContentType))
			}
			if after := accessStateSnapshot(t, h.db); after != before {
				t.Errorf("anonymous %s %s changed database or isolated files", route.Method, route.Path)
			}
		}
	}
}

func TestAccessRolesProtectSensitiveRoutesAndPermitAdminReads(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "access-admin")
	editor := h.user("editor", "access-editor")
	author := h.user("author", "access-author")
	unknown := h.user("mystery", "access-unknown")
	adminToken, editorToken, authorToken, unknownToken := h.token(admin), h.token(editor), h.token(author), h.token(unknown)
	secrets := []core.Option{
		{Key: "oauth_google_client_secret", Value: "ACCESS_TEST_OAUTH_SECRET_SENTINEL"},
		{Key: "payment_ecpay_hash_key", Value: "ACCESS_TEST_PAYMENT_SECRET_SENTINEL"},
	}
	if err := h.db.Create(&secrets).Error; err != nil {
		t.Fatalf("seed secret sentinels: %v", err)
	}
	sensitive := []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/settings", ""}, {http.MethodPost, "/api/admin/settings", `{"access_probe":"x"}`},
		{http.MethodGet, "/api/admin/oauth/providers", ""}, {http.MethodPost, "/api/admin/oauth/providers/google", `{"client_id":"probe-client","client_secret":"probe-secret","enabled":false}`},
		{http.MethodGet, "/api/admin/payment/gateways", ""}, {http.MethodPost, "/api/admin/payment/gateways/ecpay", `{"merchant_id":"probe-merchant","hash_key":"probe-key","hash_iv":"probe-iv","enabled":false}`},
		{http.MethodGet, "/api/admin/themes", ""}, {http.MethodPost, "/api/admin/themes/activate", `{"name":"default"}`},
		{http.MethodGet, "/api/admin/themes/default/settings", ""}, {http.MethodPost, "/api/admin/themes/default/settings", `{"colors":{"primary":"#123456"},"layout":{},"custom":{}}`},
		{http.MethodGet, "/api/admin/plugins", ""}, {http.MethodPost, "/api/admin/plugins/missing/reload", `{}`},
		{http.MethodGet, "/api/admin/products", ""}, {http.MethodGet, "/api/admin/orders", ""},
	}
	for _, role := range []struct{ name, token string }{{"editor", editorToken}, {"author", authorToken}, {"unknown", unknownToken}} {
		for _, item := range sensitive {
			before := accessStateSnapshot(t, h.db)
			r := h.request(item.method, item.path, item.body, role.token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, role.name+" "+item.method+" "+item.path)
			if r.code != http.StatusForbidden {
				t.Errorf("%s %s %s returned %d, want 403; body=%q", role.name, item.method, item.path, r.code, r.body)
			}
			if after := accessStateSnapshot(t, h.db); after != before {
				t.Errorf("%s %s %s changed database or isolated files", role.name, item.method, item.path)
			}
			if strings.Contains(r.body, "ACCESS_TEST_OAUTH_SECRET_SENTINEL") || strings.Contains(r.body, "ACCESS_TEST_PAYMENT_SECRET_SENTINEL") {
				t.Errorf("%s %s %s response exposed a seeded secret value", role.name, item.method, item.path)
			}
		}
	}
	for _, item := range []struct{ method, path string }{{http.MethodGet, "/api/admin/settings"}, {http.MethodGet, "/api/admin/oauth/providers"}, {http.MethodGet, "/api/admin/payment/gateways"}, {http.MethodGet, "/api/admin/themes"}, {http.MethodGet, "/api/admin/plugins"}, {http.MethodGet, "/api/admin/products"}, {http.MethodGet, "/api/admin/orders"}} {
		r := h.request(item.method, item.path, "", adminToken, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(r, "admin "+item.path)
		if r.code < 200 || r.code >= 300 {
			t.Errorf("admin GET %s returned %d, want successful representative access; body=%q", item.path, r.code, r.body)
		}
	}
}

func TestAccessPostOwnershipAndProtectedInput(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "content-admin")
	editor := h.user("editor", "content-editor")
	author := h.user("author", "content-author")
	other := h.user("author", "content-other")
	adminToken, editorToken, authorToken := h.token(admin), h.token(editor), h.token(author)
	ownPost := core.Post{Title: "Own post", Slug: "own-post", Content: "own", Type: "post", Status: "draft", AuthorID: author.ID}
	otherPost := core.Post{Title: "Other post", Slug: "other-post", Content: "other", Type: "post", Status: "draft", AuthorID: other.ID}
	ownPage := core.Post{Title: "Own page", Slug: "own-page", Content: "page", Type: "page", Status: "draft", AuthorID: author.ID}
	for _, p := range []*core.Post{&ownPost, &otherPost, &ownPage} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("create fixture post: %v", err)
		}
	}

	// Admin and editor retain the existing all-content CRUD workflow.
	for _, actor := range []struct{ name, token string }{{"admin", adminToken}, {"editor", editorToken}} {
		for _, typ := range []string{"post", "page"} {
			body := fmt.Sprintf(`{"title":"%s %s created","content":"content","type":%q,"status":"draft"}`, actor.name, typ, typ)
			created := h.request(http.MethodPost, "/api/admin/posts", body, actor.token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(created, actor.name+" create "+typ)
			if created.code < 200 || created.code >= 300 {
				t.Errorf("%s create %s returned %d; body=%q", actor.name, typ, created.code, created.body)
				continue
			}
			var got core.Post
			if err := json.Unmarshal([]byte(created.body), &got); err != nil {
				t.Errorf("decode %s-created %s: %v body=%q", actor.name, typ, err, created.body)
				continue
			}
			if got.ID == 0 {
				t.Errorf("%s-created %s has no persisted ID", actor.name, typ)
				continue
			}
			otherOwned := core.Post{Title: actor.name + " cross-owner " + typ, Slug: actor.name + "-cross-owner-" + typ, Content: "created by another author", Type: typ, Status: "draft", AuthorID: other.ID}
			if err := h.db.Create(&otherOwned).Error; err != nil {
				t.Fatalf("create cross-owner %s fixture: %v", typ, err)
			}
			crossGet := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", otherOwned.ID), "", actor.token, echo.MIMEApplicationJSON, "")
			if crossGet.code != http.StatusOK {
				t.Errorf("%s GET another author's %s returned %d body=%q", actor.name, typ, crossGet.code, crossGet.body)
			}
			crossUpdate := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", otherOwned.ID), fmt.Sprintf(`{"title":%q,"content":"changed","type":%q,"status":"published"}`, actor.name+" updated cross-owner "+typ, typ), actor.token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if crossUpdate.code != http.StatusOK {
				t.Errorf("%s PUT another author's %s returned %d body=%q", actor.name, typ, crossUpdate.code, crossUpdate.body)
			}
			var crossSaved core.Post
			if err := h.db.First(&crossSaved, otherOwned.ID).Error; err != nil {
				t.Fatalf("reload cross-owner fixture: %v", err)
			}
			if crossSaved.Title != actor.name+" updated cross-owner "+typ || crossSaved.AuthorID != other.ID {
				t.Errorf("%s cross-owner update persisted %+v", actor.name, crossSaved)
			}
			crossDelete := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/posts/%d", otherOwned.ID), "", actor.token, echo.MIMEApplicationJSON, "")
			if crossDelete.code != http.StatusOK {
				t.Errorf("%s DELETE another author's %s returned %d body=%q", actor.name, typ, crossDelete.code, crossDelete.body)
			}
			var deletedOther core.Post
			if err := h.db.First(&deletedOther, otherOwned.ID).Error; err == nil {
				t.Errorf("%s DELETE left cross-owner %s row %d", actor.name, typ, otherOwned.ID)
			}
			updated := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", got.ID), fmt.Sprintf(`{"title":"%s %s updated","content":"new","type":%q,"status":"published"}`, actor.name, typ, typ), actor.token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if updated.code < 200 || updated.code >= 300 {
				t.Errorf("%s update %s returned %d body=%q", actor.name, typ, updated.code, updated.body)
			}
			deleted := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/posts/%d", got.ID), "", actor.token, echo.MIMEApplicationJSON, "")
			if deleted.code < 200 || deleted.code >= 300 {
				t.Errorf("%s delete %s returned %d body=%q", actor.name, typ, deleted.code, deleted.body)
			}
		}
	}
	mediaType, mediaBody := accessMultipartBody(t, "file", "editor-media.png", string(acctTinyPNG(t)))
	mediaUpload := h.requestReader(http.MethodPost, "/api/admin/media", bytes.NewReader(mediaBody), editorToken, echo.MIMEApplicationJSON, mediaType)
	if mediaUpload.code != http.StatusOK {
		t.Errorf("editor media upload returned %d body=%q", mediaUpload.code, mediaUpload.body)
	}
	mediaList := h.request(http.MethodGet, "/api/admin/media", "", editorToken, echo.MIMEApplicationJSON, "")
	if mediaList.code != http.StatusOK || !strings.Contains(mediaList.body, "editor-media.png") {
		t.Errorf("editor media list returned %d body=%q", mediaList.code, mediaList.body)
	}
	mediaDelete := h.request(http.MethodDelete, "/api/admin/media/editor-media.png", "", editorToken, echo.MIMEApplicationJSON, "")
	if mediaDelete.code != http.StatusOK {
		t.Errorf("editor media delete returned %d body=%q", mediaDelete.code, mediaDelete.body)
	}

	list := h.request(http.MethodGet, "/api/admin/posts?type=post", "", authorToken, echo.MIMEApplicationJSON, "")
	if list.code != http.StatusOK {
		t.Errorf("author post list returned %d body=%q, want 200", list.code, list.body)
	} else {
		var posts []core.Post
		if err := json.Unmarshal([]byte(list.body), &posts); err != nil {
			t.Errorf("decode author post list: %v", err)
		} else {
			foundOwn := false
			for _, p := range posts {
				if p.AuthorID != author.ID || p.Type != "post" {
					t.Errorf("author post list leaked post id=%d author=%d type=%q", p.ID, p.AuthorID, p.Type)
				}
				if p.ID == ownPost.ID {
					foundOwn = true
				}
				if p.ID == ownPage.ID || p.ID == otherPost.ID {
					t.Errorf("author post list included forbidden content id=%d", p.ID)
				}
			}
			if !foundOwn {
				t.Errorf("author post list omitted own post id=%d", ownPost.ID)
			}
		}
	}
	for _, p := range []core.Post{otherPost, ownPage} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			before := core.Post{}
			if err := h.db.First(&before, p.ID).Error; err != nil {
				t.Fatalf("load protected fixture before author %s: %v", method, err)
			}
			body := `{"title":"attempted takeover","content":"changed","type":"post","status":"published"}`
			r := h.request(method, fmt.Sprintf("/api/admin/posts/%d", p.ID), body, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, fmt.Sprintf("author %s post %d", method, p.ID))
			if r.code != http.StatusNotFound {
				t.Errorf("author %s inaccessible post/page %d returned %d, want 404 body=%q", method, p.ID, r.code, r.body)
			}
			var after core.Post
			err := h.db.First(&after, p.ID).Error
			if err != nil || after.Title != before.Title || after.Content != before.Content || after.Type != before.Type || after.AuthorID != before.AuthorID {
				t.Errorf("author %s changed protected post id=%d: before=%+v after=%+v error=%v", method, p.ID, before, after, err)
				// Restore this test-owned row so later assertions do not depend on
				// the failure mode of the implementation under test.
				if err != nil {
					_ = h.db.Create(&before).Error
				} else {
					_ = h.db.Model(&core.Post{}).Where("id = ?", p.ID).Updates(map[string]any{"title": before.Title, "content": before.Content, "type": before.Type, "status": before.Status, "author_id": before.AuthorID}).Error
				}
			}
		}
	}
	pageCreate := h.request(http.MethodPost, "/api/admin/posts", `{"title":"forbidden page","content":"x","type":"page","status":"draft"}`, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if pageCreate.code != http.StatusForbidden {
		t.Errorf("author page create returned %d, want 403 body=%q", pageCreate.code, pageCreate.body)
	}
	unknown := h.user("unknown", "content-unknown")
	unknownList := h.request(http.MethodGet, "/api/admin/posts?type=post", "", h.token(unknown), echo.MIMEApplicationJSON, "")
	if unknownList.code != http.StatusForbidden {
		t.Errorf("unknown role post list returned %d, want 403 body=%q", unknownList.code, unknownList.body)
	}
	createdOwn := h.request(http.MethodPost, "/api/admin/posts", `{"title":"Author own lifecycle","content":"own content","type":"post","status":"draft"}`, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if createdOwn.code != http.StatusOK {
		t.Errorf("author own post create returned %d body=%q", createdOwn.code, createdOwn.body)
	} else {
		var createdPost core.Post
		if err := json.Unmarshal([]byte(createdOwn.body), &createdPost); err != nil {
			t.Errorf("decode author's new post: %v", err)
		} else {
			var persisted core.Post
			if err := h.db.First(&persisted, createdPost.ID).Error; err != nil {
				t.Errorf("reload author's new post: %v", err)
			} else if persisted.AuthorID != author.ID {
				t.Errorf("author-created post owner=%d, want %d", persisted.AuthorID, author.ID)
			}
			getOwn := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", createdPost.ID), "", authorToken, echo.MIMEApplicationJSON, "")
			if getOwn.code != http.StatusOK {
				t.Errorf("author GET own post returned %d body=%q", getOwn.code, getOwn.body)
			}
			updateOwn := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", createdPost.ID), `{"title":"Author own lifecycle updated","content":"updated","type":"post","status":"published"}`, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if updateOwn.code != http.StatusOK {
				t.Errorf("author PUT own post returned %d body=%q", updateOwn.code, updateOwn.body)
			}
			if err := h.db.First(&persisted, createdPost.ID).Error; err != nil {
				t.Errorf("reload updated author's post: %v", err)
			} else if persisted.Title != "Author own lifecycle updated" || persisted.AuthorID != author.ID {
				t.Errorf("updated own post = %+v", persisted)
			}
			deleteOwn := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/posts/%d", createdPost.ID), "", authorToken, echo.MIMEApplicationJSON, "")
			if deleteOwn.code != http.StatusOK {
				t.Errorf("author DELETE own post returned %d body=%q", deleteOwn.code, deleteOwn.body)
			}
			if err := h.db.First(&persisted, createdPost.ID).Error; err == nil {
				t.Errorf("author DELETE left own post %d in database", createdPost.ID)
			}
		}
	}

	otherBeforeCreate := core.User{}
	if err := h.db.First(&otherBeforeCreate, other.ID).Error; err != nil {
		t.Fatalf("reload other user before association injection: %v", err)
	}
	usersBeforeCreate := accessUsers(t, h.db)
	stateBeforeCreate := accessStateSnapshot(t, h.db)
	created := h.request(http.MethodPost, "/api/admin/posts", fmt.Sprintf(`{"id":987654321,"title":"Protected field post","content":"before","type":"post","status":"draft","author_id":%d,"author":{"id":%d,"username":"forged","email":"forged@example.test","role":"admin"},"created_at":"2001-01-01T00:00:00Z","updated_at":"2001-01-01T00:00:00Z"}`, other.ID, other.ID), authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if created.code != http.StatusBadRequest && (created.code < 200 || created.code >= 300) {
		t.Errorf("author post creation returned %d, want 400 or successful creation with protected values ignored; body=%q", created.code, created.body)
	}
	usersAfterCreate := accessUsers(t, h.db)
	if !reflect.DeepEqual(usersAfterCreate, usersBeforeCreate) {
		t.Errorf("post creation changed user rows through the Author association: before=%+v after=%+v", usersBeforeCreate, usersAfterCreate)
	}
	if created.code == http.StatusBadRequest && accessStateSnapshot(t, h.db) != stateBeforeCreate {
		t.Errorf("400 post creation response left database or isolated files changed")
	}
	if created.code >= 200 && created.code < 300 {
		var response core.Post
		if err := json.Unmarshal([]byte(created.body), &response); err != nil {
			t.Errorf("decode created post response: %v", err)
		} else {
			var persisted core.Post
			if err := h.db.First(&persisted, response.ID).Error; err != nil {
				t.Errorf("reload created post: %v", err)
			} else {
				if persisted.ID == 987654321 || persisted.AuthorID != author.ID {
					t.Errorf("created post protected values id=%d author=%d, expected server ID and actor author=%d", persisted.ID, persisted.AuthorID, author.ID)
				}
				if persisted.CreatedAt.Year() == 2001 || persisted.UpdatedAt.Year() == 2001 {
					t.Errorf("created post accepted forged timestamps: created=%v updated=%v", persisted.CreatedAt, persisted.UpdatedAt)
				}
				var otherAfterCreate core.User
				if err := h.db.First(&otherAfterCreate, other.ID).Error; err != nil {
					t.Errorf("reload other user after association injection: %v", err)
				} else if otherAfterCreate.Username != otherBeforeCreate.Username || otherAfterCreate.Email != otherBeforeCreate.Email || otherAfterCreate.Role != otherBeforeCreate.Role {
					t.Errorf("post create modified another user: before=%+v after=%+v", otherBeforeCreate, otherAfterCreate)
				}
				var actualAuthor core.User
				if err := h.db.First(&actualAuthor, persisted.AuthorID).Error; err != nil {
					t.Errorf("load persisted post author %d: %v", persisted.AuthorID, err)
				} else if actualAuthor.Username != author.Username {
					t.Errorf("persisted post author=%q, want acting user %q", actualAuthor.Username, author.Username)
				}
			}
		}
	}
	legalUpdate := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", ownPost.ID), `{"title":"Own post safely updated","content":"own updated","type":"post","status":"draft"}`, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if legalUpdate.code != http.StatusOK {
		t.Errorf("author legal own-post update returned %d body=%q, want 200", legalUpdate.code, legalUpdate.body)
	}
	var savedOwn core.Post
	if err := h.db.First(&savedOwn, ownPost.ID).Error; err != nil {
		t.Fatalf("reload author's own post: %v", err)
	}
	if savedOwn.Title != "Own post safely updated" || savedOwn.AuthorID != author.ID {
		t.Errorf("own post after legal update = %+v", savedOwn)
	}
	beforeTypeChange := core.Post{}
	if err := h.db.First(&beforeTypeChange, ownPost.ID).Error; err != nil {
		t.Fatalf("load own post before forbidden page conversion: %v", err)
	}
	pageConversion := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", ownPost.ID), `{"title":"Own post safely updated","content":"own updated","type":"page","status":"draft"}`, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if pageConversion.code != http.StatusForbidden {
		t.Errorf("author changing own post to page returned %d, want 403 body=%q", pageConversion.code, pageConversion.body)
	}
	var afterTypeChange core.Post
	if err := h.db.First(&afterTypeChange, ownPost.ID).Error; err != nil {
		t.Errorf("reload own post after forbidden page conversion: %v", err)
	}
	if !reflect.DeepEqual(afterTypeChange, beforeTypeChange) {
		t.Errorf("forbidden own-post page conversion changed database row: before=%+v after=%+v", beforeTypeChange, afterTypeChange)
		_ = h.db.Model(&core.Post{}).Where("id = ?", ownPost.ID).Updates(map[string]any{"title": beforeTypeChange.Title, "slug": beforeTypeChange.Slug, "content": beforeTypeChange.Content, "type": beforeTypeChange.Type, "status": beforeTypeChange.Status, "author_id": beforeTypeChange.AuthorID, "created_at": beforeTypeChange.CreatedAt, "updated_at": beforeTypeChange.UpdatedAt}).Error
	}
	otherSnapshot := core.Post{}
	if err := h.db.First(&otherSnapshot, otherPost.ID).Error; err != nil {
		t.Fatalf("load other post before injected-ID update: %v", err)
	}
	ownBeforeInjection := core.Post{}
	if err := h.db.First(&ownBeforeInjection, ownPost.ID).Error; err != nil {
		t.Fatalf("load own post before protected-field update: %v", err)
	}
	usersBeforeInjection := accessUsers(t, h.db)
	injected := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", ownPost.ID), fmt.Sprintf(`{"id":%d,"title":"Injected ID title","content":"injected","type":"post","status":"draft","author_id":%d,"author":{"id":%d,"username":"forged"},"created_at":"2001-01-01T00:00:00Z"}`, otherPost.ID, other.ID, other.ID), authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if injected.code != http.StatusBadRequest && injected.code != http.StatusOK {
		t.Errorf("protected-field update returned %d, want 400 or 200 body=%q", injected.code, injected.body)
	}
	var afterOwn, afterOther core.Post
	if err := h.db.First(&afterOwn, ownPost.ID).Error; err != nil {
		t.Errorf("reload own post after injection: %v", err)
	}
	if err := h.db.First(&afterOther, otherPost.ID).Error; err != nil {
		t.Errorf("reload other post after injection: %v", err)
	}
	if injected.code == http.StatusBadRequest && !reflect.DeepEqual(afterOwn, ownBeforeInjection) {
		t.Errorf("400 protected-field update changed URL row: before=%+v after=%+v", ownBeforeInjection, afterOwn)
	}
	if injected.code == http.StatusOK && (afterOwn.Title != "Injected ID title" || afterOwn.Content != "injected") {
		t.Errorf("successful protected-field update did not persist legal title/content: %+v", afterOwn)
	}
	if afterOwn.ID != ownBeforeInjection.ID || afterOwn.AuthorID != ownBeforeInjection.AuthorID || afterOwn.CreatedAt != ownBeforeInjection.CreatedAt || afterOwn.UpdatedAt.Year() == 2001 {
		t.Errorf("injected update changed protected URL row fields: before=%+v after=%+v", ownBeforeInjection, afterOwn)
	}
	if afterOwn.AuthorID == author.ID {
		var ownerAfter core.User
		if err := h.db.First(&ownerAfter, afterOwn.AuthorID).Error; err != nil {
			t.Errorf("reload URL row author after injection: %v", err)
		} else if ownerAfter.ID != author.ID || ownerAfter.Username != author.Username || ownerAfter.Email != author.Email || ownerAfter.Role != author.Role {
			t.Errorf("URL row Author association changed: before=%+v after=%+v", author, ownerAfter)
		}
	}
	if !reflect.DeepEqual(accessUsers(t, h.db), usersBeforeInjection) {
		t.Errorf("protected-field update changed user rows: before=%+v after=%+v", usersBeforeInjection, accessUsers(t, h.db))
	}
	if afterOwn.ID != ownPost.ID || afterOwn.AuthorID != author.ID {
		t.Errorf("URL-selected post after injected update = %+v", afterOwn)
	}
	if afterOther.ID != otherSnapshot.ID || afterOther.Title != otherSnapshot.Title || afterOther.AuthorID != otherSnapshot.AuthorID {
		t.Errorf("injected payload changed other post: before=%+v after=%+v", otherSnapshot, afterOther)
	}
}

func accessUsers(t *testing.T, db *gorm.DB) []core.User {
	t.Helper()
	var users []core.User
	if err := db.Order("id").Find(&users).Error; err != nil {
		t.Fatalf("read users for authorization state comparison: %v", err)
	}
	return users
}

func TestAccessJWTUsesCurrentDatabaseUserAndRole(t *testing.T) {
	h := newAccessHarness(t)
	u := h.user("admin", "jwt-current")
	token := h.token(u)
	allowed := h.request(http.MethodGet, "/api/admin/settings", "", token, echo.MIMEApplicationJSON, "")
	if allowed.code != http.StatusOK {
		t.Errorf("admin token initial GET /settings returned %d body=%q", allowed.code, allowed.body)
	}
	if err := h.db.Model(&core.User{}).Where("id = ?", u.ID).Update("role", "author").Error; err != nil {
		t.Fatalf("downgrade user: %v", err)
	}
	downgraded := h.request(http.MethodGet, "/api/admin/settings", "", token, echo.MIMEApplicationJSON, "")
	if downgraded.code != http.StatusForbidden {
		t.Errorf("old JWT after DB role downgrade returned %d, want 403 body=%q", downgraded.code, downgraded.body)
	}
	if err := h.db.Delete(&core.User{}, u.ID).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}
	deleted := h.request(http.MethodGet, "/api/admin/settings", "", token, echo.MIMEApplicationJSON, "")
	if deleted.code != http.StatusUnauthorized {
		t.Errorf("old JWT after user deletion returned %d, want 401 body=%q", deleted.code, deleted.body)
	}
}

// accessMultipartBody builds the editor upload request used to verify media
// access. Per-file ownership checks remain outside scope until the model tracks it.
func accessMultipartBody(t *testing.T, field, filename, content string) (string, []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create multipart test file: %v", err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatalf("write multipart test file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return w.FormDataContentType(), body.Bytes()
}
