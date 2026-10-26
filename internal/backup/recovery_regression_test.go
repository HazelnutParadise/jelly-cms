package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

const (
	restoreRegressionHost     = "127.0.0.1"
	restoreRegressionPort     = "55432"
	restoreRegressionUser     = "jelly_test"
	restoreRegressionPassword = "jelly-local-acceptance"
	restoreRegressionAdminDB  = "jelly_acceptance"
)

func restoreRegressionPostgres(t *testing.T) *config.DatabaseConfig {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to use the dedicated PostgreSQL container")
	}
	adminCfg := config.DatabaseConfig{Host: restoreRegressionHost, Port: restoreRegressionPort, User: restoreRegressionUser, Password: restoreRegressionPassword, Name: restoreRegressionAdminDB, Timezone: "UTC"}
	admin, err := data.Open(adminCfg)
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("open admin SQL pool: %v", err)
	}
	dbName := fmt.Sprintf("jelly_restore_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated restore test database: %v", err)
	}
	var serverVersion int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&serverVersion).Error; err != nil {
		t.Fatalf("read dedicated PostgreSQL server version: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, dbName)).Error
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)
		if serverVersion >= 130000 {
			drop += " WITH (FORCE)"
		}
		if err := admin.Exec(drop).Error; err != nil {
			t.Errorf("drop isolated restore test database: %v", err)
		}
		_ = adminSQL.Close()
	})
	cfg := &config.DatabaseConfig{Host: restoreRegressionHost, Port: restoreRegressionPort, User: restoreRegressionUser, Password: restoreRegressionPassword, Name: dbName, Timezone: "UTC"}
	db, err := data.Open(*cfg)
	if err != nil {
		t.Fatalf("open isolated restore test database: %v", err)
	}
	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated restore test database: %v", err)
	}
	previousDB, previousConfig := data.DB, config.GlobalConfig
	data.DB, config.GlobalConfig = db, &config.Config{Database: *cfg}
	t.Cleanup(func() {
		data.DB, config.GlobalConfig = previousDB, previousConfig
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return cfg
}

func restoreRegressionSecret(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestRestoreRecoveryRegressionPublicRestorePreservesFilesOnDatabaseFailure(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("write isolated recovery config: %v", err)
	}
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentsDir, "original.bin"), []byte("pre-restore attachments"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(authSecretsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, []byte(`{"settings_key":"pre-restore-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Create(&core.Option{Key: "restore-regression-sentinel", Value: "backup-value"}).Error; err != nil {
		t.Fatal(err)
	}
	const passphrase = "Restore-Regression-Password"
	id, err := Run(context.Background(), Request{Contents: []string{ContentDatabase, ContentAttachments, ContentSettings}, Passphrase: passphrase})
	if err != nil {
		t.Fatalf("create real backup fixture: %v", err)
	}

	// Change all three resource classes after the snapshot. The database version
	// mismatch makes Restore fail before it swaps either file resource.
	currentAttachment := []byte("current attachment must survive")
	currentSecrets := []byte(`{"settings_key":"current-secret-must-survive"}`)
	if err := os.WriteFile(filepath.Join(attachmentsDir, "original.bin"), currentAttachment, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, currentSecrets, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Model(&core.Option{}).Where("key = ?", "restore-regression-sentinel").Update("value", "current-value").Error; err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Exec(`INSERT INTO schema_migrations (version, name) VALUES (999, 'future-restore-regression')`).Error; err != nil {
		t.Fatalf("install the early database failure: %v", err)
	}

	_, err = Restore(context.Background(), RestoreRequest{ID: id, Confirm: RestoreConfirmation, Passphrase: passphrase})
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("Restore error = %v, want schema mismatch before filesystem swap", err)
	}
	gotAttachment, readErr := os.ReadFile(filepath.Join(attachmentsDir, "original.bin"))
	if readErr != nil || string(gotAttachment) != string(currentAttachment) {
		t.Errorf("attachment after early database failure = %q, err %v", gotAttachment, readErr)
	}
	gotSecrets, readErr := os.ReadFile(authSecretsFile)
	if readErr != nil || string(gotSecrets) != string(currentSecrets) {
		t.Errorf("authentication secrets after early database failure = %q, err %v", gotSecrets, readErr)
	}
	var option core.Option
	if err := data.DB.Where("key = ?", "restore-regression-sentinel").Take(&option).Error; err != nil || option.Value != "current-value" {
		t.Errorf("database sentinel after early restore failure = %+v, err %v", option, err)
	}
	entries, err := os.ReadDir(restoreRecoveryRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect restore journals after rollback: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("early database failure left restore journal entries: %v", entries)
	}
}

func TestRestoreRecoveryRegressionJournalRollbackRestoresOldAndMissingTargets(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentsDir, "old.bin"), []byte("old attachment"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(authSecretsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, []byte("old secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	newAttachment := []byte("new attachment")
	newSecret := []byte("new secret")
	m := Manifest{Contents: []string{ContentAttachments}, Files: []FileInfo{
		{Path: attachmentsPrefix + "new.bin", SHA256: restoreRegressionSecret(newAttachment)},
		{Path: authSecretsEntry, SHA256: restoreRegressionSecret(newSecret)},
	}}
	journal, err := prepareRestoreJournal(m)
	if err != nil || journal == "" {
		t.Fatalf("prepare restore journal = %q, %v", journal, err)
	}
	if err := os.Rename(attachmentsDir, filepath.Join(journal, "uploads-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentsDir, "new.bin"), newAttachment, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(authSecretsFile, filepath.Join(journal, "auth-secrets-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, newSecret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rollbackRestoreJournal(journal); err != nil {
		t.Fatalf("roll back interrupted file swaps: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(attachmentsDir, "old.bin")); err != nil || string(got) != "old attachment" {
		t.Errorf("old attachment after rollback = %q, err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(attachmentsDir, "new.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("new attachment survived rollback, stat error %v", err)
	}
	if got, err := os.ReadFile(authSecretsFile); err != nil || string(got) != "old secret" {
		t.Errorf("old secret after rollback = %q, err %v", got, err)
	}

	if err := os.RemoveAll("data"); err != nil {
		t.Fatal(err)
	}
	missing, err := prepareRestoreJournal(m)
	if err != nil || missing == "" {
		t.Fatalf("prepare journal with initially missing targets = %q, %v", missing, err)
	}
	if err := rollbackRestoreJournal(missing); err != nil {
		t.Fatalf("roll back prepared journal before any swaps: %v", err)
	}
	if _, err := os.Stat(attachmentsDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rollback created an originally missing attachment target: %v", err)
	}
	if _, err := os.Stat(authSecretsFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rollback created an originally missing secret target: %v", err)
	}
}

func TestRestoreRecoveryRegressionCommittedMarkerKeepsNewFiles(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("write recovery config: %v", err)
	}
	newAttachment := []byte("committed attachment")
	m := Manifest{Contents: []string{ContentAttachments}, Files: []FileInfo{{Path: attachmentsPrefix + "new.bin", SHA256: restoreRegressionSecret(newAttachment)}}}
	journal, err := prepareRestoreJournal(m)
	if err != nil || journal == "" {
		t.Fatalf("prepare committed restore journal = %q, %v", journal, err)
	}
	marker, err := restoreJournalMarkerID(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentsDir, "new.bin"), newAttachment, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRestoreCommitMarker(data.DB, marker); err != nil {
		t.Fatalf("persist database commit marker: %v", err)
	}
	if err := RecoverInterruptedRestore(); err != nil {
		t.Fatalf("recover committed database/filesystem applying journal: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(attachmentsDir, "new.bin")); err != nil || string(got) != string(newAttachment) {
		t.Errorf("committed attachment after recovery = %q, err %v", got, err)
	}
	if _, err := os.Stat(journal); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("committed recovery journal remains, stat error %v", err)
	}
	if exists, err := restoreCommitMarkerExists(data.DB, marker); err != nil || exists {
		t.Errorf("commit marker after recovery = %v, err %v, want absent", exists, err)
	}
}

func TestRestoreRecoveryRegressionUnknownJournalFailsClosed(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("write recovery config: %v", err)
	}
	journal := filepath.Join(restoreRecoveryRoot, "restore-malformed")
	if err := os.MkdirAll(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, "journal.json"), []byte(`{"phase":"unknown"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, "auth-secrets-old"), []byte("sentinel secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInterruptedRestore(); err == nil {
		t.Fatal("malformed restore journal recovered without error")
	}
	if got, err := os.ReadFile(filepath.Join(journal, "auth-secrets-old")); err != nil || string(got) != "sentinel secret" {
		t.Errorf("sentinel preserved after malformed journal failure = %q, err %v", got, err)
	}
}

func TestRestoreRecoveryRegressionRestoresSelfReferencesAcrossBatches(t *testing.T) {
	_ = restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := data.DB.Exec(`INSERT INTO product_categories (id, parent_id, name, slug, description, cover, position, hidden, created_at, updated_at)
		SELECT n, CASE WHEN n < 501 THEN n + 1 ELSE NULL END, 'restore category', 'restore-self-' || n, '', '', 0, false, now(), now()
		FROM generate_series(1, 501) AS n`).Error; err != nil {
		t.Fatalf("seed 501 forward self-references: %v", err)
	}
	if err := data.DB.Exec(`SELECT setval(pg_get_serial_sequence('product_categories','id'), 502, false)`).Error; err != nil {
		t.Fatalf("set category sequence for fixture: %v", err)
	}
	id, err := Run(context.Background(), Request{Contents: []string{ContentDatabase}})
	if err != nil {
		t.Fatalf("create database backup for self-reference restore: %v", err)
	}
	if err := data.DB.Exec(`DELETE FROM product_categories`).Error; err != nil {
		t.Fatalf("clear self-referential fixture rows: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreRequest{ID: id, Confirm: RestoreConfirmation}); err != nil {
		t.Fatalf("restore 501 self-referential rows: %v", err)
	}
	var count int64
	if err := data.DB.Raw(`SELECT count(*) FROM product_categories`).Scan(&count).Error; err != nil || count != 501 {
		t.Errorf("restored category count = %d, err %v; want 501", count, err)
	}
	var first, last struct {
		ID       uint
		ParentID *uint
	}
	if err := data.DB.Raw(`SELECT id, parent_id FROM product_categories WHERE id = 1`).Scan(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Raw(`SELECT id, parent_id FROM product_categories WHERE id = 501`).Scan(&last).Error; err != nil {
		t.Fatal(err)
	}
	if first.ParentID == nil || *first.ParentID != 2 || last.ParentID != nil {
		t.Errorf("restored self-reference endpoints = first %+v last %+v", first, last)
	}
	var deferrable bool
	if err := data.DB.Raw(`SELECT condeferrable FROM pg_constraint WHERE conname = 'product_categories_parent_id_fkey'`).Scan(&deferrable).Error; err != nil {
		t.Fatal(err)
	}
	if deferrable {
		t.Error("self-reference constraint remained DEFERRABLE after restore")
	}
}

func TestRestoreRecoveryRegressionPublicRestoreRollsBackDatabaseAndFilesAfterSwapFailure(t *testing.T) {
	_ = restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldAttachment := []byte("snapshot attachment")
	oldSecret := []byte(`{"settings_key":"snapshot-secret"}`)
	if err := os.WriteFile(filepath.Join(attachmentsDir, "state.bin"), oldAttachment, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(authSecretsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, oldSecret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Create(&core.Option{Key: "restore-regression-atomicity", Value: "snapshot-value"}).Error; err != nil {
		t.Fatal(err)
	}
	const passphrase = "Atomic-Restore-Regression"
	id, err := Run(context.Background(), Request{Contents: []string{ContentDatabase, ContentAttachments, ContentSettings}, Passphrase: passphrase})
	if err != nil {
		t.Fatalf("create atomic rollback backup: %v", err)
	}
	currentAttachment := []byte("live attachment must be restored")
	currentSecret := []byte(`{"settings_key":"live-secret-must-be-restored"}`)
	if err := os.WriteFile(filepath.Join(attachmentsDir, "state.bin"), currentAttachment, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authSecretsFile, currentSecret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Model(&core.Option{}).Where("key = ?", "restore-regression-atomicity").Update("value", "live-value").Error; err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Exec(`CREATE FUNCTION jelly_test_reject_restore_marker() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.key LIKE '__jelly_restore_commit_%' THEN RAISE EXCEPTION 'injected restore marker failure'; END IF; RETURN NEW; END; $$`).Error; err != nil {
		t.Fatalf("install failure after filesystem swap: %v", err)
	}
	if err := data.DB.Exec(`CREATE TRIGGER jelly_test_reject_restore_marker BEFORE INSERT ON options FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_restore_marker()`).Error; err != nil {
		t.Fatalf("install restore commit marker trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = data.DB.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_restore_marker ON options`)
		_ = data.DB.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_restore_marker()`)
	})

	if _, err := Restore(context.Background(), RestoreRequest{ID: id, Confirm: RestoreConfirmation, Passphrase: passphrase}); err == nil {
		t.Fatal("Restore succeeded although the commit marker insert failed after file swap")
	}
	var option core.Option
	if err := data.DB.Where("key = ?", "restore-regression-atomicity").Take(&option).Error; err != nil || option.Value != "live-value" {
		t.Errorf("database value after rollback = %+v, err %v; want live-value", option, err)
	}
	if got, err := os.ReadFile(filepath.Join(attachmentsDir, "state.bin")); err != nil || string(got) != string(currentAttachment) {
		t.Errorf("attachment after post-swap failure = %q, err %v", got, err)
	}
	if got, err := os.ReadFile(authSecretsFile); err != nil || string(got) != string(currentSecret) {
		t.Errorf("secret after post-swap failure = %q, err %v", got, err)
	}
	entries, err := os.ReadDir(restoreRecoveryRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect recovery journal after rollback: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("failed restore left recovery journals: %v", entries)
	}
}
