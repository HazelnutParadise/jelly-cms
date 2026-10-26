package data

import (
	"context"
	"testing"
	"time"
)

func TestMigrationLockRegressionAlreadyAppliedMigrationsDoNotWaitForAdvisoryLocks(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("initial MigrateDB: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelLock()
	conn, err := sqlDB.Conn(lockCtx)
	if err != nil {
		t.Fatalf("open lock-holding connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1, $2)", embeddedMigrationLockClass, 2); err != nil {
		t.Fatalf("hold migration 0002 advisory lock: %v", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", embeddedMigrationLockClass, 2); err != nil {
			t.Errorf("release migration 0002 advisory lock: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := MigrateDB(db.WithContext(ctx)); err != nil {
		t.Fatalf("MigrateDB on a fully migrated database waited on an already-applied migration lock (%s): %v", time.Since(started), err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("MigrateDB took %s while an already-applied migration lock was held", elapsed)
	}
}

func TestMigrationLockRegressionFreshDatabaseAppliesBothVersion0016Effects(t *testing.T) {
	db := newSystemMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}

	for _, table := range []string{"schema_migrations", "widgets", "widget_versions", "widget_audit_log", "widget_version_audit_log"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("fresh migration did not create %s", table)
		}
	}
	var version16 int64
	if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version = 16").Scan(&version16).Error; err != nil {
		t.Fatalf("read version 16 marker: %v", err)
	}
	if version16 != 1 {
		t.Fatalf("version 16 marker count = %d, want one marker for the grouped migration", version16)
	}
	var backfilled int64
	if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version BETWEEN 1 AND 15").Scan(&backfilled).Error; err != nil {
		t.Fatalf("read backfilled migration markers: %v", err)
	}
	if backfilled != 15 {
		t.Fatalf("backfilled migration marker count = %d, want 15", backfilled)
	}

	var widgetID int64
	if err := db.Raw("INSERT INTO widgets (name, title) VALUES (?, ?) RETURNING id", "migration-lock-probe", "Migration lock probe").Row().Scan(&widgetID); err != nil {
		t.Fatalf("insert widget through migration-created schema: %v", err)
	}
	if err := db.Exec("INSERT INTO widget_versions (widget_id, version, source) VALUES (?, 1, ?)", widgetID, "<p>snapshot</p>").Error; err != nil {
		t.Fatalf("insert widget version through migration-created schema: %v", err)
	}
	var auditRows int64
	if err := db.Raw("SELECT count(*) FROM widget_version_audit_log WHERE widget_id = ? AND action = 'INSERT'", widgetID).Scan(&auditRows).Error; err != nil {
		t.Fatalf("read widget-version audit effect: %v", err)
	}
	if auditRows != 1 {
		t.Errorf("version 0016 widget-version audit trigger wrote %d rows, want 1", auditRows)
	}

	if err := MigrateDB(db); err != nil {
		t.Fatalf("repeat MigrateDB after version 0016: %v", err)
	}
	var version16After int64
	if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version = 16").Scan(&version16After).Error; err != nil {
		t.Fatalf("read version 16 marker after rerun: %v", err)
	}
	if version16After != version16 {
		t.Errorf("version 16 marker count changed from %d to %d after rerun", version16, version16After)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = 16").Error; err != nil {
		t.Fatalf("simulate a missing marker after version 0016 side effects: %v", err)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("retry version 0016 with its marker missing: %v", err)
	}
	var retriedMarker, retriedAuditRows int64
	if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version = 16").Scan(&retriedMarker).Error; err != nil {
		t.Fatalf("read retried version 16 marker: %v", err)
	}
	if err := db.Raw("SELECT count(*) FROM widget_version_audit_log WHERE widget_id = ? AND action = 'INSERT'", widgetID).Scan(&retriedAuditRows).Error; err != nil {
		t.Fatalf("read audit rows after version 0016 retry: %v", err)
	}
	if retriedMarker != 1 || retriedAuditRows != 1 {
		t.Errorf("retry with a missing version marker left marker=%d and audit rows=%d, want one each", retriedMarker, retriedAuditRows)
	}
}
