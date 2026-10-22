package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"gorm.io/gorm"
)

const (
	bootHost               = "127.0.0.1"
	bootPort               = "55432"
	bootUser               = "jelly_test"
	bootPassword           = "jelly-local-acceptance"
	bootAdminDatabase      = "jelly_acceptance"
	bootPendingPath        = "data/install-pending.json"
	bootConfigPath         = "data/config.json"
	bootSiteTitleKey       = "site_title"
	bootInstallationIDKey  = "installation_id"
	bootCommittedSiteTitle = "Recovered Boot Site"
	bootExistingSiteTitle  = "Existing Boot Site"
)

var bootDatabaseCounter atomic.Int64

type bootPending struct {
	Database       config.DatabaseConfig `json:"database"`
	InstallationID string                `json:"installation_id"`
}

func requireBootPostgres(t *testing.T) {
	t.Helper()

	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run tests against the dedicated PostgreSQL container")
	}
}

func bootAdminConfig() config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     bootHost,
		Port:     bootPort,
		User:     bootUser,
		Password: bootPassword,
		Name:     bootAdminDatabase,
		Timezone: "UTC",
	}
}

func bootDatabaseConfig(name string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     bootHost,
		Port:     bootPort,
		User:     bootUser,
		Password: bootPassword,
		Name:     name,
		Timezone: "UTC",
	}
}

func bootClose(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return
	}
	_ = sqlDB.Close()
}

func openBootHandle(t *testing.T, cfg config.DatabaseConfig) *gorm.DB {
	t.Helper()

	db, err := data.Open(cfg)
	if err != nil {
		t.Fatalf("open PostgreSQL handle for %s/%s: %v", cfg.Host, cfg.Name, err)
	}
	t.Cleanup(func() { bootClose(db) })
	return db
}

func createBootDatabase(t *testing.T, admin *gorm.DB) string {
	t.Helper()

	name := fmt.Sprintf("jelly_boot_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), bootDatabaseCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)).Error; err != nil {
		t.Fatalf("create test database %s: %v", name, err)
	}

	var serverVersion int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&serverVersion).Error; err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}

	t.Cleanup(func() {
		if !strings.HasPrefix(name, "jelly_boot_") {
			t.Errorf("refusing to drop database %q because it is not owned by this test", name)
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

func useBootGlobals(t *testing.T) {
	t.Helper()

	previousConfig := config.GlobalConfig
	previousDB := data.DB

	config.GlobalConfig = nil
	data.DB = nil

	t.Cleanup(func() {
		current := data.DB
		config.GlobalConfig = previousConfig
		data.DB = previousDB
		if current != nil && current != previousDB {
			bootClose(current)
		}
	})
}

func bootWritePending(t *testing.T, pending bootPending) []byte {
	t.Helper()

	payload, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		t.Fatalf("encode the pending installation journal: %v", err)
	}
	if err := os.MkdirAll("data", 0700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	if err := os.WriteFile(bootPendingPath, payload, 0600); err != nil {
		t.Fatalf("write %s: %v", bootPendingPath, err)
	}
	return payload
}

func bootRequireFileAbsent(t *testing.T, name string) {
	t.Helper()

	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s stat error = %v, want the file to be absent", name, err)
	}
}

func bootRequireFileUnchanged(t *testing.T, name string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed:\ngot:  %s\nwant: %s", name, got, want)
	}
}

func bootRequireConfig(t *testing.T, want config.DatabaseConfig) {
	t.Helper()

	info, err := os.Stat(bootConfigPath)
	if err != nil {
		t.Fatalf("stat %s after bootstrap: %v", bootConfigPath, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want a regular file", bootConfigPath, info.Mode())
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("%s permissions = %#o, want 0600", bootConfigPath, mode)
	}

	payload, err := os.ReadFile(bootConfigPath)
	if err != nil {
		t.Fatalf("read %s after bootstrap: %v", bootConfigPath, err)
	}
	var cfg config.Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatalf("decode %s after bootstrap: %v", bootConfigPath, err)
	}
	if cfg.Database != want {
		t.Errorf("saved database config = %+v, want %+v", cfg.Database, want)
	}
}

func bootOption(t *testing.T, db *gorm.DB, key string) (string, bool) {
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

func bootAdminUsers(t *testing.T, db *gorm.DB) []core.User {
	t.Helper()

	var users []core.User
	if err := db.Where("role = ?", "admin").Order("id").Find(&users).Error; err != nil {
		t.Fatalf("query admin users: %v", err)
	}
	return users
}

func TestInitializeSiteWithoutConfigStaysUninstalled(t *testing.T) {
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	if err := initializeSite(); err != nil {
		t.Fatalf("initializeSite() = %v, want nil when no configuration exists", err)
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v, want nil without a configuration", config.GlobalConfig)
	}
	if data.DB != nil {
		t.Errorf("data.DB = %p, want nil without a configuration", data.DB)
	}
	if _, err := os.Stat("data"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("data directory stat error = %v, want no data directory for a no-op bootstrap", err)
	}
	if install.IsInstalled() {
		t.Errorf("IsInstalled() = true without any configuration or journal, want false")
	}
}

func TestInitializeSiteRejectsCorruptConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	if err := os.MkdirAll("data", 0700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	payload := []byte(`{"database": {"host":`)
	if err := os.WriteFile(bootConfigPath, payload, 0600); err != nil {
		t.Fatalf("write the corrupt configuration: %v", err)
	}

	if err := initializeSite(); err == nil {
		t.Fatalf("initializeSite() = nil for a corrupt configuration, want an error")
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v after a corrupt configuration, want nil", config.GlobalConfig)
	}
	if data.DB != nil {
		t.Errorf("data.DB = %p after a corrupt configuration, want nil", data.DB)
	}
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false with a corrupt config file on disk, want true so installation stays closed")
	}
	bootRequireFileUnchanged(t, bootConfigPath, payload)
}

func TestInitializeSiteKeepsConfigWhenDatabaseUnreachable(t *testing.T) {
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig("jelly_boot_unreachable")
	cfg.Port = "1"
	if err := config.Save(&config.Config{Database: cfg}); err != nil {
		t.Fatalf("save the configuration with an unreachable database: %v", err)
	}
	written, err := os.ReadFile(bootConfigPath)
	if err != nil {
		t.Fatalf("read %s before bootstrap: %v", bootConfigPath, err)
	}

	if err := initializeSite(); err == nil {
		t.Fatalf("initializeSite() = nil although the configured database is unreachable, want an error")
	}
	if config.GlobalConfig == nil {
		t.Fatalf("config.GlobalConfig = nil after loading valid configuration, want it retained")
	}
	if config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig.Database = %+v, want %+v", config.GlobalConfig.Database, cfg)
	}
	if data.DB != nil {
		t.Errorf("data.DB = %p after a failed connection, want nil", data.DB)
	}
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false with a valid configuration and an unreachable database, want true")
	}
	bootRequireFileUnchanged(t, bootConfigPath, written)
}

func TestInitializeSiteRecoversCommittedInstallation(t *testing.T) {
	requireBootPostgres(t)

	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the committed schema: %v", err)
	}

	storedAdmin := core.User{
		Username: "admin",
		Email:    "admin@example.com",
		Password: "stored-password-hash",
		Role:     "admin",
		Provider: "local",
	}
	if err := seed.Create(&storedAdmin).Error; err != nil {
		t.Fatalf("create the committed administrator: %v", err)
	}
	if err := seed.Create(&core.Option{Key: bootSiteTitleKey, Value: bootCommittedSiteTitle}).Error; err != nil {
		t.Fatalf("create the committed site title: %v", err)
	}

	pending := bootPending{Database: cfg, InstallationID: "boot-recovery-committed"}
	if err := seed.Create(&core.Option{Key: bootInstallationIDKey, Value: pending.InstallationID}).Error; err != nil {
		t.Fatalf("create the committed installation marker: %v", err)
	}
	bootWritePending(t, pending)

	if err := initializeSite(); err != nil {
		t.Fatalf("initializeSite() = %v, want nil for a committed installation journal", err)
	}

	bootRequireFileAbsent(t, bootPendingPath)
	bootRequireConfig(t, cfg)
	if config.GlobalConfig == nil {
		t.Fatalf("config.GlobalConfig = nil after recovery, want the recovered configuration")
	}
	if config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig.Database = %+v, want %+v", config.GlobalConfig.Database, cfg)
	}
	if data.DB == nil {
		t.Fatalf("data.DB = nil after recovery, want a usable connection")
	}
	if data.DB == seed {
		t.Errorf("data.DB was replaced by the fixture handle, want the recovered connection")
	}

	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		t.Fatalf("query the database behind the published handle: %v", err)
	}
	if current != database {
		t.Errorf("published data.DB is connected to %q, want %q", current, database)
	}

	admins := bootAdminUsers(t, data.DB)
	if len(admins) != 1 {
		t.Fatalf("admin user count through the published handle = %d, want exactly 1", len(admins))
	}
	if admins[0].Username != storedAdmin.Username || admins[0].Email != storedAdmin.Email || admins[0].Password != storedAdmin.Password {
		t.Errorf("published database admin = %+v, want the committed %+v", admins[0], storedAdmin)
	}
	if title, ok := bootOption(t, data.DB, bootSiteTitleKey); !ok || title != bootCommittedSiteTitle {
		t.Errorf("site_title through the published handle = %q (found %v), want %q", title, ok, bootCommittedSiteTitle)
	}
	if marker, ok := bootOption(t, data.DB, bootInstallationIDKey); !ok || marker != pending.InstallationID {
		t.Errorf("installation marker through the published handle = %q (found %v), want %q", marker, ok, pending.InstallationID)
	}
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false after recovery, want true")
	}

	firstConfig, err := os.ReadFile(bootConfigPath)
	if err != nil {
		t.Fatalf("read %s after the first bootstrap: %v", bootConfigPath, err)
	}
	firstDB := data.DB

	if err := initializeSite(); err != nil {
		t.Fatalf("second initializeSite() = %v, want nil for an already installed site", err)
	}
	if data.DB != firstDB {
		t.Errorf("second initializeSite() replaced data.DB: got %p, want %p", data.DB, firstDB)
	}
	bootRequireFileUnchanged(t, bootConfigPath, firstConfig)
	bootRequireFileAbsent(t, bootPendingPath)
}

func TestInitializeSiteDiscardsJournalForFreshDatabase(t *testing.T) {
	requireBootPostgres(t)

	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)
	bootWritePending(t, bootPending{Database: cfg, InstallationID: "boot-recovery-uncommitted"})

	if err := initializeSite(); err != nil {
		t.Fatalf("initializeSite() = %v, want nil for a journal whose transaction never committed", err)
	}

	bootRequireFileAbsent(t, bootPendingPath)
	if install.IsInstalled() {
		t.Errorf("IsInstalled() = true after discarding an uncommitted journal, want false")
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v after discarding an uncommitted journal, want nil", config.GlobalConfig)
	}
	if data.DB != nil {
		t.Errorf("data.DB = %p after discarding an uncommitted journal, want nil", data.DB)
	}
	bootRequireFileAbsent(t, bootConfigPath)

	var tables int64
	if err := seed.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&tables).Error; err != nil {
		t.Fatalf("count the tables of the fresh database: %v", err)
	}
	if tables != 0 {
		t.Errorf("fresh database %s has %d tables after discarding the journal, want 0", database, tables)
	}
}

func TestInitializeSiteMigrationFailureKeepsSiteUnavailable(t *testing.T) {
	requireBootPostgres(t)

	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)

	// GORM's PostgreSQL migrator only treats table_type = 'BASE TABLE' as an
	// existing table. A view that occupies the users name is therefore not
	// migrated; AutoMigrate tries CREATE TABLE users and PostgreSQL rejects it.
	if err := seed.Exec("CREATE VIEW users AS SELECT 1 AS id").Error; err != nil {
		t.Fatalf("create the conflicting users view: %v", err)
	}

	if err := config.Save(&config.Config{Database: cfg}); err != nil {
		t.Fatalf("save the configuration pointing at the conflicting schema: %v", err)
	}
	written, err := os.ReadFile(bootConfigPath)
	if err != nil {
		t.Fatalf("read %s before bootstrap: %v", bootConfigPath, err)
	}

	err = initializeSite()
	if err == nil {
		t.Fatalf("initializeSite() = nil although the database migration must fail, want an error")
	}
	if !strings.Contains(err.Error(), "migrate the database") {
		t.Fatalf("initializeSite() error = %v, want a database migration failure", err)
	}
	if data.DB != nil {
		t.Errorf("data.DB = %p after a failed migration, want nil so the site is not served as ready", data.DB)
	}
	if config.GlobalConfig == nil || config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig = %+v after a failed migration, want the retained %+v", config.GlobalConfig, cfg)
	}
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false with a valid configuration after a failed migration, want true")
	}
	bootRequireFileUnchanged(t, bootConfigPath, written)
}

func TestInitializeSiteExistingConfigBootsUsableDatabase(t *testing.T) {
	requireBootPostgres(t)

	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)
	if err := config.Save(&config.Config{Database: cfg}); err != nil {
		t.Fatalf("save the existing configuration: %v", err)
	}
	written, err := os.ReadFile(bootConfigPath)
	if err != nil {
		t.Fatalf("read %s before bootstrap: %v", bootConfigPath, err)
	}

	if err := initializeSite(); err != nil {
		t.Fatalf("initializeSite() = %v, want nil for a valid existing configuration", err)
	}
	if data.DB == nil {
		t.Fatalf("data.DB = nil after a successful bootstrap, want a usable connection")
	}
	if data.DB == seed {
		t.Errorf("data.DB was replaced by the fixture handle, want the bootstrap connection")
	}

	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		t.Fatalf("query the database behind the published handle: %v", err)
	}
	if current != database {
		t.Errorf("published data.DB is connected to %q, want %q", current, database)
	}

	var usersTable bool
	if err := data.DB.Raw("SELECT to_regclass('users') IS NOT NULL").Scan(&usersTable).Error; err != nil {
		t.Fatalf("check the migrated users table: %v", err)
	}
	if !usersTable {
		t.Errorf("users table was not migrated on the published handle")
	}

	if config.GlobalConfig == nil || config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig = %+v after a successful bootstrap, want %+v", config.GlobalConfig, cfg)
	}
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false after a successful bootstrap, want true")
	}
	bootRequireFileUnchanged(t, bootConfigPath, written)
}

func TestInitializeSiteKeepsJournalForMismatchedMarker(t *testing.T) {
	requireBootPostgres(t)

	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)

	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the existing schema: %v", err)
	}
	if err := seed.Create(&core.Option{Key: bootSiteTitleKey, Value: bootExistingSiteTitle}).Error; err != nil {
		t.Fatalf("create the existing site title: %v", err)
	}
	if err := seed.Create(&core.Option{Key: bootInstallationIDKey, Value: "database-marker"}).Error; err != nil {
		t.Fatalf("create the existing installation marker: %v", err)
	}

	written := bootWritePending(t, bootPending{Database: cfg, InstallationID: "journal-marker"})

	if err := initializeSite(); err == nil {
		t.Fatalf("initializeSite() = nil although the database marker belongs to a different installation, want an error")
	}

	bootRequireFileUnchanged(t, bootPendingPath, written)
	if data.DB != nil {
		t.Errorf("data.DB = %p after refusing the journal, want nil", data.DB)
	}
	if config.GlobalConfig != nil {
		t.Errorf("config.GlobalConfig = %+v after refusing the journal, want nil", config.GlobalConfig)
	}
	bootRequireFileAbsent(t, bootConfigPath)
	if !install.IsInstalled() {
		t.Errorf("IsInstalled() = false while the journal is still present, want true")
	}

	if marker, ok := bootOption(t, seed, bootInstallationIDKey); !ok || marker != "database-marker" {
		t.Errorf("installation marker after refusing the journal = %q (found %v), want %q", marker, ok, "database-marker")
	}
	if title, ok := bootOption(t, seed, bootSiteTitleKey); !ok || title != bootExistingSiteTitle {
		t.Errorf("site_title after refusing the journal = %q (found %v), want %q", title, ok, bootExistingSiteTitle)
	}
}
