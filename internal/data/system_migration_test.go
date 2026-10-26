package data

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"gorm.io/gorm"
)

const (
	sysmigHost    = "127.0.0.1"
	sysmigPort    = "55432"
	sysmigUser    = "jelly_test"
	sysmigPass    = "jelly-local-acceptance"
	sysmigAdminDB = "jelly_acceptance"
	sysmigPrefix  = "jelly_system_migration_"
)

var sysmigDBCounter atomic.Int64

// newSystemMigrationDB returns an empty isolated database. The caller runs
// MigrateDB itself, so each test controls when migrations run.
func newSystemMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run the system migration tests against the dedicated PostgreSQL container")
	}
	admin, err := Open(config.DatabaseConfig{Host: sysmigHost, Port: sysmigPort, User: sysmigUser, Password: sysmigPass, Name: sysmigAdminDB, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	dbName := fmt.Sprintf("%s%d_%d", sysmigPrefix, os.Getpid(), sysmigDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated system migration database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, sysmigPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated system migration database %s: %v", dbName, err)
		}
	})
	db, err := Open(config.DatabaseConfig{Host: sysmigHost, Port: sysmigPort, User: sysmigUser, Password: sysmigPass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated system migration database: %v", err)
	}
	t.Cleanup(func() { sysmigCloseDB(db) })
	return db
}

func sysmigCloseDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// sysmigSchemaSnapshot describes the tables, columns and indexes of the
// current schema, so two runs can be compared.
func sysmigSchemaSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var columns, indexes []string
	if err := db.Raw(`SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
		FROM information_schema.columns WHERE table_schema = current_schema()`).Scan(&columns).Error; err != nil {
		t.Fatalf("snapshot columns: %v", err)
	}
	if err := db.Raw(`SELECT indexname || ' ' || indexdef FROM pg_indexes WHERE schemaname = current_schema()`).Scan(&indexes).Error; err != nil {
		t.Fatalf("snapshot indexes: %v", err)
	}
	sort.Strings(columns)
	sort.Strings(indexes)
	return strings.Join(columns, "\n") + "\n--\n" + strings.Join(indexes, "\n")
}

func TestSystemMigrationsIdempotentRerun(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("first MigrateDB: %v", err)
	}
	first := sysmigSchemaSnapshot(t, db)
	var rowsBefore int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations").Scan(&rowsBefore).Error; err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}

	for run := 2; run <= 3; run++ {
		if err := MigrateDB(db); err != nil {
			t.Fatalf("MigrateDB run %d on a migrated database: %v", run, err)
		}
	}
	if again := sysmigSchemaSnapshot(t, db); again != first {
		t.Errorf("re-running the migrations changed the schema")
	}
	var rowsAfter int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations").Scan(&rowsAfter).Error; err != nil {
		t.Fatalf("count schema_migrations after reruns: %v", err)
	}
	if rowsAfter != rowsBefore {
		t.Errorf("schema_migrations grew from %d to %d rows on rerun", rowsBefore, rowsAfter)
	}
}

func TestSchemaMigrationsRecordsVersions(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}
	var versions []int
	if err := db.Raw("SELECT version FROM schema_migrations ORDER BY version").Scan(&versions).Error; err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	have := map[int]bool{}
	for _, v := range versions {
		have[v] = true
	}
	required := []int{1}
	for v := 16; v <= 23; v++ {
		required = append(required, v)
	}
	for _, v := range required {
		if !have[v] {
			t.Errorf("schema_migrations has no row for version %d; rows=%v", v, versions)
		}
	}
	// 0015 belongs to ticket 09 and may be absent from this tree.
}

func TestStartRefusesNewerSchema(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}
	if err := db.Exec("INSERT INTO schema_migrations (version, name) VALUES (999, 'future_migration.sql')").Error; err != nil {
		t.Fatalf("insert future version: %v", err)
	}
	err := MigrateDB(db)
	if err == nil {
		t.Fatalf("MigrateDB accepted a database at schema version 999; want an error")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("MigrateDB error %q does not name version 999", err)
	}
}

func TestSystemMigrationConstraints(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}
	mustFail := func(t *testing.T, what, statement string, args ...any) {
		t.Helper()
		if err := db.Exec(statement, args...).Error; err == nil {
			t.Errorf("%s: insert was accepted, want a constraint error", what)
		}
	}
	mustPass := func(t *testing.T, what, statement string, args ...any) {
		t.Helper()
		if err := db.Exec(statement, args...).Error; err != nil {
			t.Errorf("%s: insert was rejected: %v", what, err)
		}
	}

	t.Run("redirect kind is 301, 302 or 410", func(t *testing.T) {
		mustFail(t, "kind 500", "INSERT INTO redirects (from_path, to_url, kind) VALUES ('/bad-kind', '/x', 500)")
		mustPass(t, "kind 301", "INSERT INTO redirects (from_path, to_url, kind) VALUES ('/good-kind', '/x', 301)")
	})

	t.Run("redirect source is unique case-insensitively for non-rules", func(t *testing.T) {
		mustPass(t, "first /Dup", "INSERT INTO redirects (from_path, to_url, kind) VALUES ('/Dup', '/a', 301)")
		mustFail(t, "/dup again", "INSERT INTO redirects (from_path, to_url, kind) VALUES ('/dup', '/b', 301)")
		mustPass(t, "wildcard rule with the same path", "INSERT INTO redirects (from_path, to_url, kind, is_rule) VALUES ('/Dup', '/c', 301, TRUE)")
	})

	t.Run("not_found_log path_hash is unique and paths carry no query", func(t *testing.T) {
		mustPass(t, "first hash", "INSERT INTO not_found_log (path, path_hash) VALUES ('/a', 'hash-1')")
		mustFail(t, "same hash", "INSERT INTO not_found_log (path, path_hash) VALUES ('/b', 'hash-1')")
		mustFail(t, "path with a query", "INSERT INTO not_found_log (path, path_hash) VALUES ('/c?x=1', 'hash-2')")
	})

	t.Run("jobs dedupe_key is unique and nullable", func(t *testing.T) {
		mustPass(t, "first null key", "INSERT INTO jobs (queue, kind) VALUES ('mail', 'mail.send')")
		mustPass(t, "second null key", "INSERT INTO jobs (queue, kind) VALUES ('mail', 'mail.send')")
		mustPass(t, "first named key", "INSERT INTO jobs (queue, kind, dedupe_key) VALUES ('schedule', 'logs.prune', 'logs.prune:20261023T0300Z')")
		mustFail(t, "same named key", "INSERT INTO jobs (queue, kind, dedupe_key) VALUES ('schedule', 'logs.prune', 'logs.prune:20261023T0300Z')")
	})

	t.Run("system_logs level and source are checked", func(t *testing.T) {
		mustFail(t, "level 9", "INSERT INTO system_logs (level, source, message) VALUES (9, 'app', 'x')")
		mustFail(t, "unknown source", "INSERT INTO system_logs (level, source, message) VALUES (1, 'bogus', 'x')")
		mustPass(t, "valid row", "INSERT INTO system_logs (level, source, message) VALUES (1, 'app', 'x')")
	})

	t.Run("indexes exist", func(t *testing.T) {
		var names []string
		if err := db.Raw("SELECT indexname FROM pg_indexes WHERE schemaname = current_schema()").Scan(&names).Error; err != nil {
			t.Fatalf("list indexes: %v", err)
		}
		have := map[string]bool{}
		for _, n := range names {
			have[n] = true
		}
		for _, want := range []string{
			"idx_system_logs_at",
			"idx_system_logs_source_at",
			"idx_system_logs_level_at",
			"idx_system_logs_request_id",
			"idx_jobs_queue_state_run_at",
			"idx_redirects_from_path_lower",
			"idx_not_found_log_last_seen",
		} {
			if !have[want] {
				t.Errorf("index %s is missing", want)
			}
		}
	})
}
