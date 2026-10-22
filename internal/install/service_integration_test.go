package install

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	integrationHost     = "127.0.0.1"
	integrationPort     = "55432"
	integrationUser     = "jelly_test"
	integrationPassword = "jelly-local-acceptance"
	integrationDatabase = "jelly_acceptance"
	integrationConfig   = "data/config.json"
)

var integrationDatabaseCounter atomic.Int64

// requireIntegrationPostgres keeps the database tests opt-in so that a normal
// go test run never depends on a running PostgreSQL server.
func requireIntegrationPostgres(t *testing.T) {
	t.Helper()

	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run tests against the dedicated PostgreSQL container")
	}
}

func integrationAdminConfig() config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     integrationDatabase,
		Timezone: "UTC",
	}
}

func integrationRequest(database string) InstallRequest {
	return InstallRequest{
		DBHost:     integrationHost,
		DBPort:     integrationPort,
		DBUser:     integrationUser,
		DBPass:     integrationPassword,
		DBName:     database,
		DBTimezone: "UTC",

		SiteTitle:  "Integration Site",
		AdminUser:  "admin",
		AdminEmail: "admin@example.com",
		AdminPass:  "Str0ng-Passw0rd!",
	}
}

func closeIntegrationHandle(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return
	}
	_ = sqlDB.Close()
}

func openIntegrationHandle(t *testing.T, cfg config.DatabaseConfig) *gorm.DB {
	t.Helper()

	db, err := data.Open(cfg)
	if err != nil {
		t.Fatalf("open PostgreSQL handle for %s/%s: %v", cfg.Host, cfg.Name, err)
	}
	t.Cleanup(func() { closeIntegrationHandle(db) })
	return db
}

// createIntegrationDatabase creates a database that only this test owns. The
// cleanup refuses names outside the jelly_it_ prefix and removes the database
// together with any connection the test still holds.
func createIntegrationDatabase(t *testing.T, admin *gorm.DB) string {
	t.Helper()

	name := fmt.Sprintf("jelly_it_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), integrationDatabaseCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)).Error; err != nil {
		t.Fatalf("create test database %s: %v", name, err)
	}

	var serverVersion int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&serverVersion).Error; err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}

	t.Cleanup(func() {
		if !strings.HasPrefix(name, "jelly_it_") {
			t.Errorf("refusing to drop database %q because it is not owned by this integration test", name)
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

// useIntegrationGlobals restores the package globals after the test and gives
// the test a distinct sentinel handle so replacing data.DB is observable.
func useIntegrationGlobals(t *testing.T) *gorm.DB {
	t.Helper()

	previousConfig := config.GlobalConfig
	previousDB := data.DB

	sentinel := openIntegrationHandle(t, integrationAdminConfig())
	config.GlobalConfig = nil
	data.DB = sentinel

	t.Cleanup(func() {
		current := data.DB
		config.GlobalConfig = previousConfig
		data.DB = previousDB

		closed := make(map[*gorm.DB]bool)
		closeOnce := func(db *gorm.DB) {
			if db == nil || db == previousDB || closed[db] {
				return
			}
			closed[db] = true
			closeIntegrationHandle(db)
		}
		closeOnce(current)
		closeOnce(sentinel)
	})

	return sentinel
}

func integrationAdminUsers(t *testing.T, db *gorm.DB) []core.User {
	t.Helper()

	var users []core.User
	if err := db.Where("role = ?", "admin").Order("id").Find(&users).Error; err != nil {
		t.Fatalf("query admin users: %v", err)
	}
	return users
}

func integrationOption(t *testing.T, db *gorm.DB, key string) (string, bool) {
	t.Helper()

	var options []core.Option
	if err := db.Where("key = ?", key).Limit(1).Find(&options).Error; err != nil {
		t.Fatalf("query option %q: %v", key, err)
	}
	if len(options) == 0 {
		return "", false
	}
	return options[0].Value, true
}

func integrationConfigMode(t *testing.T) os.FileMode {
	t.Helper()

	info, err := os.Stat(integrationConfig)
	if err != nil {
		t.Fatalf("stat %s after installation: %v", integrationConfig, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want a regular file", integrationConfig, info.Mode())
	}
	return info.Mode().Perm()
}

func requireInstalledState(t *testing.T, db *gorm.DB, req InstallRequest) {
	t.Helper()

	if config.GlobalConfig == nil {
		t.Errorf("config.GlobalConfig = nil after a successful installation, want the installed configuration to be usable immediately")
	} else if config.GlobalConfig.Database.Name != req.DBName {
		t.Errorf("config.GlobalConfig.Database.Name = %q, want %q", config.GlobalConfig.Database.Name, req.DBName)
	}

	if data.DB == nil {
		t.Fatalf("data.DB = nil after a successful installation, want a usable connection immediately")
	}

	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		t.Fatalf("query the database behind the global handle: %v", err)
	}
	if current != req.DBName {
		t.Errorf("data.DB is connected to %q, want %q", current, req.DBName)
	}

	if !IsInstalled() {
		t.Errorf("IsInstalled() = false after a successful installation, want true")
	}

	admins := integrationAdminUsers(t, db)
	if len(admins) != 1 {
		t.Fatalf("admin user count = %d, want exactly 1", len(admins))
	}
	admin := admins[0]
	if admin.Username != req.AdminUser {
		t.Errorf("admin username = %q, want %q", admin.Username, req.AdminUser)
	}
	if admin.Email != req.AdminEmail {
		t.Errorf("admin email = %q, want %q", admin.Email, req.AdminEmail)
	}
	if admin.Role != "admin" {
		t.Errorf("admin role = %q, want %q", admin.Role, "admin")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.AdminPass)); err != nil {
		t.Errorf("stored admin password is not a bcrypt hash of the requested password: %v", err)
	}

	title, ok := integrationOption(t, db, "site_title")
	if !ok {
		t.Fatalf("option site_title is missing after a successful installation")
	}
	if title != req.SiteTitle {
		t.Errorf("site_title = %q, want %q", title, req.SiteTitle)
	}

	if mode := integrationConfigMode(t); mode != 0600 {
		t.Errorf("%s permissions = %#o, want 0600", integrationConfig, mode)
	}
}

// TestIntegrationConnectionSuccessKeepsTheActiveHandle verifies the public
// contract of TestConnection: a successful probe must not replace the database
// handle the running site uses.
func TestIntegrationConnectionSuccessKeepsTheActiveHandle(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	existing := useIntegrationGlobals(t)

	if err := TestConnection(integrationRequest(database)); err != nil {
		t.Fatalf("TestConnection() = %v, want nil for the dedicated database that accepts this connection", err)
	}
	if data.DB != existing {
		t.Errorf("TestConnection() replaced the active data.DB with %p, want the previous handle %p", data.DB, existing)
	}
}

// TestIntegrationInstallationPublishesACompleteSite verifies the result of one
// installation through the same globals the server uses at runtime.
func TestIntegrationInstallationPublishesACompleteSite(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	req := integrationRequest(database)

	if err := PerformInstallation(req); err != nil {
		t.Fatalf("PerformInstallation() = %v, want nil for a valid request against the dedicated database", err)
	}
	if data.DB == sentinel {
		t.Errorf("PerformInstallation() left data.DB on the previous handle, want the new site database")
	}

	requireInstalledState(t, data.DB, req)
}

// TestIntegrationSecondInstallationKeepsTheFirstSite verifies that installing
// again cannot replace the administrator or the site settings.
func TestIntegrationSecondInstallationKeepsTheFirstSite(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	useIntegrationGlobals(t)
	first := integrationRequest(database)

	if err := PerformInstallation(first); err != nil {
		t.Fatalf("first PerformInstallation() = %v, want nil", err)
	}
	requireInstalledState(t, data.DB, first)

	originalAdmins := integrationAdminUsers(t, data.DB)
	originalAdmin := originalAdmins[0]
	originalTitle, ok := integrationOption(t, data.DB, "site_title")
	if !ok {
		t.Fatalf("site_title is missing after the first installation")
	}
	originalConfig, err := os.ReadFile(integrationConfig)
	if err != nil {
		t.Fatalf("read %s after the first installation: %v", integrationConfig, err)
	}

	second := first
	second.SiteTitle = "Replaced Site"
	second.AdminUser = "intruder"
	second.AdminEmail = "intruder@example.com"
	second.AdminPass = "Another-Passw0rd!"

	if err := PerformInstallation(second); err == nil {
		t.Errorf("second PerformInstallation() = nil, want an error because the site is already installed")
	}

	admins := integrationAdminUsers(t, data.DB)
	if len(admins) != 1 {
		t.Fatalf("admin user count after the second installation = %d, want exactly 1", len(admins))
	}
	if admins[0].ID != originalAdmin.ID ||
		admins[0].Username != originalAdmin.Username ||
		admins[0].Email != originalAdmin.Email ||
		admins[0].Password != originalAdmin.Password {
		t.Errorf("second installation changed the administrator: got %+v, want the original %+v", admins[0], originalAdmin)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admins[0].Password), []byte(first.AdminPass)); err != nil {
		t.Errorf("first password stopped verifying after the second installation: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admins[0].Password), []byte(second.AdminPass)); err == nil {
		t.Errorf("second installation replaced the administrator password")
	}

	title, ok := integrationOption(t, data.DB, "site_title")
	if !ok {
		t.Fatalf("site_title disappeared after the second installation")
	}
	if title != originalTitle {
		t.Errorf("site_title after the second installation = %q, want the original %q", title, originalTitle)
	}
	if title != first.SiteTitle {
		t.Errorf("site_title after the second installation = %q, want %q", title, first.SiteTitle)
	}

	currentConfig, err := os.ReadFile(integrationConfig)
	if err != nil {
		t.Fatalf("read %s after the second installation: %v", integrationConfig, err)
	}
	if string(currentConfig) != string(originalConfig) {
		t.Errorf("second installation rewrote %s:\ngot:  %s\nwant: %s", integrationConfig, currentConfig, originalConfig)
	}
}

// TestIntegrationConcurrentInstallationsCreateOneSite verifies that two
// simultaneous installations cannot both succeed or create two administrators.
func TestIntegrationConcurrentInstallationsCreateOneSite(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	useIntegrationGlobals(t)
	seed := openIntegrationHandle(t, config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     database,
		Timezone: "UTC",
	})
	req := integrationRequest(database)

	start := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index] = PerformInstallation(req)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("concurrent installations returned %d successes and %d errors, want exactly 1 success and 1 error; errors: %v", successes, len(results)-successes, results)
	}
	if data.DB == nil {
		t.Fatalf("data.DB = nil after concurrent installations although one succeeded")
	}

	admins := integrationAdminUsers(t, seed)
	if len(admins) != 1 {
		t.Errorf("admin user count after concurrent installations = %d, want exactly 1", len(admins))
	}
	if title, ok := integrationOption(t, seed, "site_title"); !ok || title != req.SiteTitle {
		t.Errorf("site_title after concurrent installations = %q (found %v), want %q", title, ok, req.SiteTitle)
	}
	if mode := integrationConfigMode(t); mode != 0600 {
		t.Errorf("%s permissions after concurrent installations = %#o, want 0600", integrationConfig, mode)
	}
}

// TestIntegrationOptionFailureLeavesNoHalfInstall makes the options insert fail
// with a PostgreSQL trigger and verifies that the administrator, site title,
// globals and config file are all absent so the installation can be retried.
func TestIntegrationOptionFailureLeavesNoHalfInstall(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	seed := openIntegrationHandle(t, config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     database,
		Timezone: "UTC",
	})
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the schema for the failure scenario: %v", err)
	}

	if err := seed.Exec(`
		CREATE OR REPLACE FUNCTION jelly_it_block_options_insert() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'options insert blocked by integration test';
		END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatalf("create the trigger function: %v", err)
	}
	if err := seed.Exec(`
		CREATE TRIGGER jelly_it_block_options
		BEFORE INSERT ON options
		FOR EACH ROW EXECUTE FUNCTION jelly_it_block_options_insert()`).Error; err != nil {
		t.Fatalf("create the options trigger: %v", err)
	}

	req := integrationRequest(database)
	firstErr := PerformInstallation(req)
	if firstErr == nil {
		t.Errorf("PerformInstallation() = nil although the options trigger made site_title fail, want an error")
	}

	admins := integrationAdminUsers(t, seed)
	if len(admins) != 0 {
		t.Errorf("admin user count after the failed installation = %d, want 0 because the installation must be atomic", len(admins))
	}
	if title, ok := integrationOption(t, seed, "site_title"); ok {
		t.Errorf("site_title = %q after the failed installation, want it absent", title)
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v after the failed installation, want it unpublished", config.GlobalConfig)
	}
	if data.DB != sentinel {
		t.Errorf("data.DB was replaced by the failed installation: got %p, want the previous handle %p", data.DB, sentinel)
	}
	if _, err := os.Stat(integrationConfig); !os.IsNotExist(err) {
		t.Errorf("%s exists after the failed installation (stat error: %v), want no config file", integrationConfig, err)
	}

	if err := seed.Exec("DROP TRIGGER jelly_it_block_options ON options").Error; err != nil {
		t.Fatalf("drop the options trigger before retrying: %v", err)
	}
	if err := seed.Exec("DROP FUNCTION jelly_it_block_options_insert()").Error; err != nil {
		t.Fatalf("drop the trigger function before retrying: %v", err)
	}

	if err := PerformInstallation(req); err != nil {
		t.Fatalf("PerformInstallation() retry after removing the trigger = %v, want nil", err)
	}
	requireInstalledState(t, data.DB, req)
}

// TestIntegrationConfigWriteFailureLeavesNoHalfInstall blocks the config path
// with a regular file and verifies that no usable installation is left behind
// and that the same request succeeds once the obstacle is removed.
func TestIntegrationConfigWriteFailureLeavesNoHalfInstall(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	seed := openIntegrationHandle(t, config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     database,
		Timezone: "UTC",
	})
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the schema for the config failure scenario: %v", err)
	}

	if err := os.WriteFile("data", []byte("not a directory"), 0600); err != nil {
		t.Fatalf("create the regular file that blocks the config directory: %v", err)
	}

	req := integrationRequest(database)
	firstErr := PerformInstallation(req)
	if firstErr == nil {
		t.Errorf("PerformInstallation() = nil although data is a regular file and the config cannot be written, want an error")
	}

	admins := integrationAdminUsers(t, seed)
	if len(admins) != 0 {
		t.Errorf("admin user count after the config write failure = %d, want 0 because the installation must be atomic", len(admins))
	}
	if title, ok := integrationOption(t, seed, "site_title"); ok {
		t.Errorf("site_title = %q after the config write failure, want it absent", title)
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v after the config write failure, want it unpublished", config.GlobalConfig)
	}
	if data.DB != sentinel {
		t.Errorf("data.DB was replaced by the failed installation: got %p, want the previous handle %p", data.DB, sentinel)
	}
	if IsInstalled() {
		t.Errorf("IsInstalled() = true after the config write failure, want no usable half installation")
	}
	if _, err := os.Stat(integrationConfig); err == nil {
		t.Errorf("%s exists after the config write failure, want no config file", integrationConfig)
	}

	if err := os.Remove("data"); err != nil {
		t.Fatalf("remove the file that blocked the config directory: %v", err)
	}

	if err := PerformInstallation(req); err != nil {
		t.Fatalf("PerformInstallation() retry after removing the config obstacle = %v, want nil", err)
	}
	requireInstalledState(t, data.DB, req)
}
