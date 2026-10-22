package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

const (
	recoveryPendingPath  = "data/install-pending.json"
	recoveryConfigPath   = "data/config.json"
	recoveryMarkerKey    = "installation_id"
	recoverySiteTitleKey = "site_title"
)

// recoveryPending mirrors the public persistence contract of the installation
// journal. It is declared here instead of reusing the private type so that a
// change to the on-disk field names is caught by the recovery tests.
type recoveryPending struct {
	Database       config.DatabaseConfig `json:"database"`
	InstallationID string                `json:"installation_id"`
}

func recoveryDatabaseConfig(name string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     name,
		Timezone: "UTC",
	}
}

func writeRecoveryPending(t *testing.T, pending recoveryPending) []byte {
	t.Helper()

	payload, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		t.Fatalf("encode the pending installation journal: %v", err)
	}
	if err := os.MkdirAll("data", 0700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	if err := os.WriteFile(recoveryPendingPath, payload, 0600); err != nil {
		t.Fatalf("write %s: %v", recoveryPendingPath, err)
	}
	return payload
}

func decodeRecoveryPending(t *testing.T, payload []byte) recoveryPending {
	t.Helper()

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode the pending installation journal fields: %v", err)
	}
	if _, ok := fields["database"]; !ok {
		t.Fatalf("pending installation journal has no %q field", "database")
	}
	if _, ok := fields["installation_id"]; !ok {
		t.Fatalf("pending installation journal has no %q field", "installation_id")
	}

	var pending recoveryPending
	if err := json.Unmarshal(payload, &pending); err != nil {
		t.Fatalf("decode the pending installation journal: %v", err)
	}
	return pending
}

func requireRecoveryFileAbsent(t *testing.T, name string) {
	t.Helper()

	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s stat error = %v, want the file to be absent", name, err)
	}
}

func requireRecoveryFileUnchanged(t *testing.T, name string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed:\ngot:  %s\nwant: %s", name, got, want)
	}
}

func requireRecoveryGlobals(t *testing.T, wantDB *gorm.DB, wantConfig *config.Config) {
	t.Helper()

	if data.DB != wantDB {
		t.Errorf("data.DB = %p, want the untouched handle %p", data.DB, wantDB)
	}
	if config.GlobalConfig != wantConfig {
		t.Errorf("config.GlobalConfig = %p, want the untouched value %p", config.GlobalConfig, wantConfig)
	}
}

func requireRecoveryConfig(t *testing.T, want config.DatabaseConfig) {
	t.Helper()

	info, err := os.Stat(recoveryConfigPath)
	if err != nil {
		t.Fatalf("stat %s after recovery: %v", recoveryConfigPath, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want a regular file", recoveryConfigPath, info.Mode())
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("%s permissions = %#o, want 0600", recoveryConfigPath, mode)
	}

	payload, err := os.ReadFile(recoveryConfigPath)
	if err != nil {
		t.Fatalf("read %s after recovery: %v", recoveryConfigPath, err)
	}
	var cfg config.Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatalf("decode %s after recovery: %v", recoveryConfigPath, err)
	}
	if cfg.Database != want {
		t.Errorf("saved database config = %+v, want the pending database %+v", cfg.Database, want)
	}
}

// TestRecoveryClearsJournalWhenTransactionNeverCommitted covers a crash before
// the installation transaction created anything: a brand-new database with no
// tables at all still has a journal on disk. Recovery must discard the journal,
// publish nothing and leave the site installable.
func TestRecoveryClearsJournalWhenTransactionNeverCommitted(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	cfg := recoveryDatabaseConfig(database)

	probe := openIntegrationHandle(t, cfg)
	var tables int64
	if err := probe.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&tables).Error; err != nil {
		t.Fatalf("count the tables of the fresh database: %v", err)
	}
	if tables != 0 {
		t.Fatalf("fresh database %s has %d tables, want 0 so the journal can only belong to a rolled-back transaction", database, tables)
	}

	writeRecoveryPending(t, recoveryPending{Database: cfg, InstallationID: "recovery-rolled-back"})

	if err := RecoverInstallation(); err != nil {
		t.Fatalf("RecoverInstallation() = %v, want nil for a journal whose transaction never created any table", err)
	}

	requireRecoveryFileAbsent(t, recoveryPendingPath)
	requireRecoveryGlobals(t, sentinel, nil)
	if IsInstalled() {
		t.Errorf("IsInstalled() = true after the journal was discarded, want false")
	}
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s stat error = %v, want no config file after discarding the journal", recoveryConfigPath, err)
	}

	req := integrationRequest(database)
	if err := PerformInstallation(req); err != nil {
		t.Fatalf("PerformInstallation() after recovery = %v, want nil so the site can be installed again", err)
	}
	requireInstalledState(t, data.DB, req)
}

// TestRecoveryClearsJournalWhenSchemaExistsWithoutMarker covers a database that
// was migrated but never committed a site. The empty schema is not evidence of
// an installation, so the journal must be removed without publishing globals.
func TestRecoveryClearsJournalWhenSchemaExistsWithoutMarker(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	cfg := recoveryDatabaseConfig(database)

	seed := openIntegrationHandle(t, cfg)
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the empty schema: %v", err)
	}
	var users, options int64
	if err := seed.Model(&core.User{}).Count(&users).Error; err != nil {
		t.Fatalf("count the users of the fresh schema: %v", err)
	}
	if err := seed.Model(&core.Option{}).Count(&options).Error; err != nil {
		t.Fatalf("count the options of the fresh schema: %v", err)
	}
	if users != 0 || options != 0 {
		t.Fatalf("fresh schema has %d users and %d options, want an empty schema", users, options)
	}

	writeRecoveryPending(t, recoveryPending{Database: cfg, InstallationID: "recovery-empty-schema"})

	if err := RecoverInstallation(); err != nil {
		t.Fatalf("RecoverInstallation() = %v, want nil for a migrated database without an installation marker", err)
	}

	requireRecoveryFileAbsent(t, recoveryPendingPath)
	requireRecoveryGlobals(t, sentinel, nil)
	if IsInstalled() {
		t.Errorf("IsInstalled() = true after discarding an empty schema journal, want false")
	}
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s stat error = %v, want no config file for an empty schema", recoveryConfigPath, err)
	}

	if err := seed.Model(&core.User{}).Count(&users).Error; err != nil {
		t.Fatalf("count the users after recovery: %v", err)
	}
	if err := seed.Model(&core.Option{}).Count(&options).Error; err != nil {
		t.Fatalf("count the options after recovery: %v", err)
	}
	if users != 0 || options != 0 {
		t.Errorf("database has %d users and %d options after recovery, want the empty schema untouched", users, options)
	}
}

// TestRecoveryCompletesCommittedInstallationAndIsIdempotent covers the crash
// window after the transaction committed but before the config file was saved.
// Recovery must store the config with 0600, publish usable globals, remove the
// journal, and do nothing on a second call.
func TestRecoveryCompletesCommittedInstallationAndIsIdempotent(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	cfg := recoveryDatabaseConfig(database)

	seed := openIntegrationHandle(t, cfg)
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the committed schema: %v", err)
	}

	pending := recoveryPending{Database: cfg, InstallationID: "recovery-committed"}
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
	if err := seed.Create(&core.Option{Key: recoverySiteTitleKey, Value: "Recovered Site"}).Error; err != nil {
		t.Fatalf("create the committed site title: %v", err)
	}
	if err := seed.Create(&core.Option{Key: recoveryMarkerKey, Value: pending.InstallationID}).Error; err != nil {
		t.Fatalf("create the committed installation marker: %v", err)
	}

	writeRecoveryPending(t, pending)
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s stat error = %v, want no config file before recovery", recoveryConfigPath, err)
	}

	if err := RecoverInstallation(); err != nil {
		t.Fatalf("RecoverInstallation() = %v, want nil for a committed installation with a matching marker", err)
	}

	requireRecoveryFileAbsent(t, recoveryPendingPath)
	requireRecoveryConfig(t, cfg)

	if config.GlobalConfig == nil {
		t.Fatalf("config.GlobalConfig = nil after recovery, want the recovered configuration")
	}
	if config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig.Database = %+v, want %+v", config.GlobalConfig.Database, cfg)
	}
	if data.DB == nil {
		t.Fatalf("data.DB = nil after recovery, want a usable connection")
	}
	if data.DB == sentinel {
		t.Errorf("data.DB was not replaced by recovery, want the recovered database")
	}

	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		t.Fatalf("query the database behind the recovered handle: %v", err)
	}
	if current != database {
		t.Errorf("recovered data.DB is connected to %q, want %q", current, database)
	}
	if title, ok := integrationOption(t, data.DB, recoverySiteTitleKey); !ok || title != "Recovered Site" {
		t.Errorf("site_title through the recovered globals = %q (found %v), want %q", title, ok, "Recovered Site")
	}
	admins := integrationAdminUsers(t, data.DB)
	if len(admins) != 1 {
		t.Fatalf("admin user count through the recovered globals = %d, want exactly 1", len(admins))
	}
	if admins[0].Username != storedAdmin.Username || admins[0].Email != storedAdmin.Email || admins[0].Password != storedAdmin.Password {
		t.Errorf("recovery changed the committed administrator: got %+v, want the stored %+v", admins[0], storedAdmin)
	}
	if marker, ok := integrationOption(t, data.DB, recoveryMarkerKey); !ok || marker != pending.InstallationID {
		t.Errorf("installation marker through the recovered globals = %q (found %v), want %q", marker, ok, pending.InstallationID)
	}
	if !IsInstalled() {
		t.Errorf("IsInstalled() = false after recovery, want true")
	}

	firstConfig, err := os.ReadFile(recoveryConfigPath)
	if err != nil {
		t.Fatalf("read %s after the first recovery: %v", recoveryConfigPath, err)
	}
	firstDB := data.DB
	firstLoaded := config.GlobalConfig

	if err := RecoverInstallation(); err != nil {
		t.Fatalf("second RecoverInstallation() = %v, want nil for an already recovered installation", err)
	}

	requireRecoveryFileAbsent(t, recoveryPendingPath)
	requireRecoveryFileUnchanged(t, recoveryConfigPath, firstConfig)
	requireRecoveryConfig(t, cfg)
	if data.DB != firstDB {
		t.Errorf("second recovery replaced data.DB: got %p, want %p", data.DB, firstDB)
	}
	if config.GlobalConfig != firstLoaded {
		t.Errorf("second recovery replaced config.GlobalConfig: got %p, want %p", config.GlobalConfig, firstLoaded)
	}
}

// TestRecoveryKeepsJournalWhenDatabaseUnreachable verifies that a database
// outage is not treated as a rolled-back transaction: the journal must stay
// byte-for-byte and the running globals must not change.
func TestRecoveryKeepsJournalWhenDatabaseUnreachable(t *testing.T) {
	requireIntegrationPostgres(t)

	t.Chdir(t.TempDir())
	sentinel := useIntegrationGlobals(t)

	pending := recoveryPending{
		Database: config.DatabaseConfig{
			Host:     integrationHost,
			Port:     "1",
			User:     integrationUser,
			Password: integrationPassword,
			Name:     "jelly_unreachable",
			Timezone: "UTC",
		},
		InstallationID: "recovery-unreachable",
	}
	written := writeRecoveryPending(t, pending)

	if err := RecoverInstallation(); err == nil {
		t.Fatalf("RecoverInstallation() = nil although the pending database is unreachable, want an error")
	}

	requireRecoveryFileUnchanged(t, recoveryPendingPath, written)
	requireRecoveryGlobals(t, sentinel, nil)
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s stat error = %v, want no config file while the journal is unresolved", recoveryConfigPath, err)
	}
	if !IsInstalled() {
		t.Errorf("IsInstalled() = false while the journal is still present, want true so installation cannot be reopened")
	}
}

// TestRecoveryRefusesMismatchedMarker verifies that a database carrying a
// different installation marker is never overwritten by the journal.
func TestRecoveryRefusesMismatchedMarker(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	cfg := recoveryDatabaseConfig(database)

	seed := openIntegrationHandle(t, cfg)
	if err := data.MigrateDB(seed); err != nil {
		t.Fatalf("pre-create the database schema: %v", err)
	}
	storedAdmin := core.User{
		Username: "existing-admin",
		Email:    "existing@example.com",
		Password: "existing-password-hash",
		Role:     "admin",
		Provider: "local",
	}
	if err := seed.Create(&storedAdmin).Error; err != nil {
		t.Fatalf("create the existing administrator: %v", err)
	}
	if err := seed.Create(&core.Option{Key: recoverySiteTitleKey, Value: "Existing Site"}).Error; err != nil {
		t.Fatalf("create the existing site title: %v", err)
	}
	if err := seed.Create(&core.Option{Key: recoveryMarkerKey, Value: "database-marker"}).Error; err != nil {
		t.Fatalf("create the existing installation marker: %v", err)
	}

	written := writeRecoveryPending(t, recoveryPending{Database: cfg, InstallationID: "journal-marker"})

	if err := RecoverInstallation(); err == nil {
		t.Fatalf("RecoverInstallation() = nil although the database marker belongs to a different installation, want an error")
	}

	requireRecoveryFileUnchanged(t, recoveryPendingPath, written)
	requireRecoveryGlobals(t, sentinel, nil)
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s stat error = %v, want no config file after refusing the journal", recoveryConfigPath, err)
	}

	if marker, ok := integrationOption(t, seed, recoveryMarkerKey); !ok || marker != "database-marker" {
		t.Errorf("installation marker after refusing recovery = %q (found %v), want %q", marker, ok, "database-marker")
	}
	if title, ok := integrationOption(t, seed, recoverySiteTitleKey); !ok || title != "Existing Site" {
		t.Errorf("site_title after refusing recovery = %q (found %v), want %q", title, ok, "Existing Site")
	}
	admins := integrationAdminUsers(t, seed)
	if len(admins) != 1 {
		t.Fatalf("admin user count after refusing recovery = %d, want exactly 1", len(admins))
	}
	if admins[0].Username != storedAdmin.Username || admins[0].Password != storedAdmin.Password {
		t.Errorf("refusing recovery changed the administrator: got %+v, want the stored %+v", admins[0], storedAdmin)
	}
}

// TestRecoveryRefusesJournalForDifferentDatabase verifies that an existing
// config describing another database wins over the journal: neither file is
// rewritten.
func TestRecoveryRefusesJournalForDifferentDatabase(t *testing.T) {
	requireIntegrationPostgres(t)

	t.Chdir(t.TempDir())
	sentinel := useIntegrationGlobals(t)

	existing := config.Config{Database: config.DatabaseConfig{
		Host:     integrationHost,
		Port:     integrationPort,
		User:     integrationUser,
		Password: integrationPassword,
		Name:     "jelly_existing_site",
		Timezone: "UTC",
	}}
	if err := config.Save(&existing); err != nil {
		t.Fatalf("save the existing configuration: %v", err)
	}
	existingPayload, err := os.ReadFile(recoveryConfigPath)
	if err != nil {
		t.Fatalf("read the existing configuration: %v", err)
	}

	pending := recoveryPending{
		Database: config.DatabaseConfig{
			Host:     integrationHost,
			Port:     integrationPort,
			User:     integrationUser,
			Password: integrationPassword,
			Name:     "jelly_pending_site",
			Timezone: "UTC",
		},
		InstallationID: "recovery-other-database",
	}
	written := writeRecoveryPending(t, pending)

	if err := RecoverInstallation(); err == nil {
		t.Fatalf("RecoverInstallation() = nil although the pending journal targets a different database than the existing config, want an error")
	}

	requireRecoveryFileUnchanged(t, recoveryPendingPath, written)
	requireRecoveryFileUnchanged(t, recoveryConfigPath, existingPayload)
	requireRecoveryGlobals(t, sentinel, nil)
}

// TestRecoveryKeepsJournalWrittenByInstallation pins the on-disk journal that
// PerformInstallation leaves after a post-commit config failure: the database
// field, the installation_id field and the database marker must agree before
// recovery finishes the installation.
func TestRecoveryKeepsJournalWrittenByInstallation(t *testing.T) {
	requireIntegrationPostgres(t)

	admin := openIntegrationHandle(t, integrationAdminConfig())
	database := createIntegrationDatabase(t, admin)
	t.Chdir(t.TempDir())

	sentinel := useIntegrationGlobals(t)
	cfg := recoveryDatabaseConfig(database)

	seed := openIntegrationHandle(t, cfg)
	if err := os.MkdirAll("data", 0700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	if err := os.Mkdir(recoveryConfigPath, 0700); err != nil {
		t.Fatalf("create the directory that blocks the config file: %v", err)
	}

	req := integrationRequest(database)
	if err := PerformInstallation(req); err == nil {
		t.Fatalf("PerformInstallation() = nil although the config file cannot be replaced, want an error")
	}
	requireRecoveryGlobals(t, sentinel, nil)

	payload, err := os.ReadFile(recoveryPendingPath)
	if err != nil {
		t.Fatalf("read the journal left by PerformInstallation: %v", err)
	}
	if bytes.Contains(payload, []byte(req.AdminPass)) {
		t.Errorf("the journal contains the administrator password in clear text")
	}
	pending := decodeRecoveryPending(t, payload)
	if pending.Database != cfg {
		t.Errorf("journal database = %+v, want %+v", pending.Database, cfg)
	}
	if pending.InstallationID == "" {
		t.Fatalf("journal installation_id is empty, want the committed marker")
	}
	if marker, ok := integrationOption(t, seed, recoveryMarkerKey); !ok || marker != pending.InstallationID {
		t.Errorf("database marker = %q (found %v), want the journal installation_id %q", marker, ok, pending.InstallationID)
	}
	if title, ok := integrationOption(t, seed, recoverySiteTitleKey); !ok || title != req.SiteTitle {
		t.Errorf("committed site_title = %q (found %v), want %q", title, ok, req.SiteTitle)
	}
	if admins := integrationAdminUsers(t, seed); len(admins) != 1 {
		t.Fatalf("committed admin user count = %d, want exactly 1", len(admins))
	}

	if err := os.Remove(recoveryConfigPath); err != nil {
		t.Fatalf("remove the directory blocking the config file: %v", err)
	}

	if err := RecoverInstallation(); err != nil {
		t.Fatalf("RecoverInstallation() for the journal written by PerformInstallation = %v, want nil", err)
	}

	requireRecoveryFileAbsent(t, recoveryPendingPath)
	requireRecoveryConfig(t, cfg)
	if config.GlobalConfig == nil || config.GlobalConfig.Database != cfg {
		t.Errorf("config.GlobalConfig = %+v after recovery, want the recovered database", config.GlobalConfig)
	}
	if data.DB == nil || data.DB == sentinel {
		t.Fatalf("data.DB = %p after recovery, want a freshly published database", data.DB)
	}
	var current string
	if err := data.DB.Raw("SELECT current_database()").Scan(&current).Error; err != nil {
		t.Fatalf("query the recovered database: %v", err)
	}
	if current != database {
		t.Errorf("recovered data.DB is connected to %q, want %q", current, database)
	}
}

// TestRecoveryKeepsMalformedJournal covers a truncated or invalid journal: the
// file must stay for inspection and the site must conservatively stay closed.
func TestRecoveryKeepsMalformedJournal(t *testing.T) {
	t.Chdir(t.TempDir())

	previousConfig := config.GlobalConfig
	previousDB := data.DB
	config.GlobalConfig = nil
	data.DB = nil
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		data.DB = previousDB
	})

	payload := []byte(`{"database": {"host":`)
	if err := os.MkdirAll("data", 0700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	if err := os.WriteFile(recoveryPendingPath, payload, 0600); err != nil {
		t.Fatalf("write the malformed journal: %v", err)
	}

	if err := RecoverInstallation(); err == nil {
		t.Fatalf("RecoverInstallation() = nil for a malformed journal, want an error")
	}

	requireRecoveryFileUnchanged(t, recoveryPendingPath, payload)
	if !IsInstalled() {
		t.Errorf("IsInstalled() = false with a malformed journal on disk, want true so installation is not reopened")
	}
	if _, err := os.Stat(recoveryConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s stat error = %v, want no config file for a malformed journal", recoveryConfigPath, err)
	}
}
