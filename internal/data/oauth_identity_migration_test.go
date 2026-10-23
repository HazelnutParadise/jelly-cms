package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	oauthMigrationTestHost    = "127.0.0.1"
	oauthMigrationTestPort    = "55432"
	oauthMigrationTestUser    = "jelly_test"
	oauthMigrationTestPass    = "jelly-local-acceptance"
	oauthMigrationTestAdminDB = "jelly_acceptance"
	oauthMigrationTestPrefix  = "jelly_oauth_migration_"
)

var oauthMigrationTestDBCounter atomic.Int64

func TestOAuthIdentityMigration(t *testing.T) {
	db := newOAuthIdentityMigrationDB(t)
	userBefore, postBefore := readOAuthLegacyRows(t, db)

	for i := 0; i < 2; i++ {
		if err := MigrateDB(db); err != nil {
			t.Fatalf("MigrateDB run %d: %v", i+1, err)
		}
	}

	for _, table := range []string{"oauth_identities", "oauth_identity_audit_log"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("MigrateDB returned nil but required table %q does not exist", table)
		}
	}

	identityColumns := map[string]bool{
		"id": false, "user_id": false, "provider": false, "subject": false,
		"created_at": false, "updated_at": false, "deleted_at": false,
	}
	var columns []string
	if err := db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'oauth_identities'").Scan(&columns).Error; err != nil {
		t.Fatalf("read oauth_identities columns: %v", err)
	}
	for _, column := range columns {
		if _, ok := identityColumns[column]; ok {
			identityColumns[column] = true
		}
	}
	for column, found := range identityColumns {
		if !found {
			t.Errorf("oauth_identities is missing required column %q; columns are %v", column, columns)
		}
	}
	var identityIDType, createdAtType, updatedAtType, deletedAtType, deletedAtNullable string
	if err := db.Raw(`SELECT
		max(data_type) FILTER (WHERE column_name = 'id'),
		max(data_type) FILTER (WHERE column_name = 'created_at'),
		max(data_type) FILTER (WHERE column_name = 'updated_at')
		FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'oauth_identities'`).Row().Scan(&identityIDType, &createdAtType, &updatedAtType); err != nil {
		t.Fatalf("inspect identity column types: %v", err)
	}
	if err := db.Raw(`SELECT data_type, is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'oauth_identities' AND column_name = 'deleted_at'`).Row().Scan(&deletedAtType, &deletedAtNullable); err != nil {
		t.Fatalf("inspect deleted_at type and nullability: %v", err)
	}
	if identityIDType != "bigint" || createdAtType != "timestamp with time zone" || updatedAtType != "timestamp with time zone" || deletedAtType != "timestamp with time zone" || deletedAtNullable != "YES" {
		t.Errorf("identity columns do not use bigint/timestamptz with nullable deleted_at: id=%q created_at=%q updated_at=%q deleted_at=%q nullable=%q", identityIDType, createdAtType, updatedAtType, deletedAtType, deletedAtNullable)
	}

	auditColumns := map[string]bool{
		"id": false, "identity_id": false, "user_id": false, "provider": false,
		"action": false, "old_state": false, "new_state": false, "actor_id": false,
		"actor_email": false, "created_at": false,
	}
	columns = nil
	if err := db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'oauth_identity_audit_log'").Scan(&columns).Error; err != nil {
		t.Fatalf("read oauth_identity_audit_log columns: %v", err)
	}
	for _, column := range columns {
		if _, ok := auditColumns[column]; ok {
			auditColumns[column] = true
		}
	}
	for column, found := range auditColumns {
		if !found {
			t.Errorf("oauth_identity_audit_log is missing required column %q; columns are %v", column, columns)
		}
	}
	if strings.Contains(strings.Join(columns, " "), "subject") {
		t.Error("oauth_identity_audit_log must not persist OAuth subjects")
	}

	userAfter, postAfter := readOAuthLegacyRows(t, db)
	if !reflect.DeepEqual(userBefore, userAfter) {
		t.Errorf("pre-migration User fields changed after repeated migration:\nbefore=%+v\nafter=%+v", userBefore, userAfter)
	}
	if !reflect.DeepEqual(postBefore, postAfter) {
		t.Errorf("pre-migration Post fields or ownership changed after repeated migration:\nbefore=%+v\nafter=%+v", postBefore, postAfter)
	}
	var migratedIdentityCount int64
	if err := db.Raw("SELECT count(*) FROM oauth_identities WHERE user_id = ?", userBefore.ID).Scan(&migratedIdentityCount).Error; err != nil {
		t.Fatalf("check that migration did not infer identities from legacy users: %v", err)
	}
	if migratedIdentityCount != 0 {
		t.Errorf("migration inferred %d OAuth identities for a legacy user; want none", migratedIdentityCount)
	}

	var secondUserID int64
	if err := db.Raw(`INSERT INTO users (username, email, password, avatar_url, role, provider, created_at, updated_at)
		VALUES ('second-author', 'second@example.test', '', '', 'author', 'local', now(), now()) RETURNING id`).Row().Scan(&secondUserID); err != nil {
		t.Fatalf("create second test user: %v", err)
	}
	var thirdUserID int64
	if err := db.Raw(`INSERT INTO users (username, email, password, avatar_url, role, provider, created_at, updated_at)
		VALUES ('third-author', 'third@example.test', '', '', 'author', 'local', now(), now()) RETURNING id`).Row().Scan(&thirdUserID); err != nil {
		t.Fatalf("create third test user: %v", err)
	}
	var fourthUserID int64
	if err := db.Raw(`INSERT INTO users (username, email, password, avatar_url, role, provider, created_at, updated_at)
		VALUES ('fourth-author', 'fourth@example.test', '', '', 'author', 'local', now(), now()) RETURNING id`).Row().Scan(&fourthUserID); err != nil {
		t.Fatalf("create fourth test user: %v", err)
	}
	var fifthUserID int64
	if err := db.Raw(`INSERT INTO users (username, email, password, avatar_url, role, provider, created_at, updated_at)
		VALUES ('fifth-author', 'fifth@example.test', '', '', 'author', 'local', now(), now()) RETURNING id`).Row().Scan(&fifthUserID); err != nil {
		t.Fatalf("create actor-only test user: %v", err)
	}

	insertIdentity := func(userID int64, provider, subject string) int64 {
		t.Helper()
		var id int64
		err := db.Raw(`INSERT INTO oauth_identities (user_id, provider, subject)
			VALUES (?, ?, ?) RETURNING id`, userID, provider, subject).Row().Scan(&id)
		if err != nil {
			t.Fatalf("insert identity for user %d (%s): %v", userID, provider, err)
		}
		return id
	}
	assertRejected := func(label, wantSQLState string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s unexpectedly succeeded", label)
			return
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Errorf("%s failed for a non-PostgreSQL constraint reason: %v", label, err)
		} else if pgErr.Code != wantSQLState {
			t.Errorf("%s failed with SQLSTATE %s, want constraint violation %s: %v", label, pgErr.Code, wantSQLState, err)
		}
	}

	var id int64
	var createdAt, updatedAt time.Time
	if err := db.Raw(`INSERT INTO oauth_identities (user_id, provider, subject)
		VALUES (?, 'google', 'Subject-X') RETURNING id, created_at, updated_at`, userBefore.ID).Row().Scan(&id, &createdAt, &updatedAt); err != nil {
		t.Fatalf("insert identity using database defaults: %v", err)
	}
	if id <= 0 || createdAt.IsZero() || updatedAt.IsZero() || createdAt.Location() == nil || updatedAt.Location() == nil {
		t.Errorf("identity defaults did not provide a generated id and timestamps: id=%d created_at=%v updated_at=%v", id, createdAt, updatedAt)
	}
	assertRejected("duplicate active provider/subject", "23505", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', 'Subject-X')`, secondUserID).Error)
	assertRejected("second active subject for one user/provider", "23505", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', 'Subject-Y')`, userBefore.ID).Error)
	if otherProviderID := insertIdentity(userBefore.ID, "github", "Subject-X"); otherProviderID <= 0 {
		t.Error("same subject under a different provider did not create a distinct identity")
	}
	caseVariantID := insertIdentity(secondUserID, "google", "subject-x")
	if caseVariantID <= 0 {
		t.Error("provider/subject matching was not case-sensitive")
	}
	if differentUserID := insertIdentity(thirdUserID, "google", "different-subject"); differentUserID <= 0 {
		t.Error("different users could not link distinct subjects for the same provider")
	}

	assertRejected("empty subject", "23514", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', '')`, thirdUserID).Error)
	assertRejected("whitespace-only subject", "23514", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', E' \t\n  ')`, thirdUserID).Error)
	assertRejected("unsupported provider", "23514", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'local', 'valid-subject')`, thirdUserID).Error)
	assertRejected("identity for missing user", "23503", db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (9223372036854770000, 'google', 'orphan-subject')`).Error)
	longSubjectErr := db.Exec(`INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', ?)`, fourthUserID, strings.Repeat("x", 256)).Error
	if longSubjectErr == nil {
		t.Error("256-character subject unexpectedly succeeded")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(longSubjectErr, &pgErr) || (pgErr.Code != "22001" && pgErr.Code != "23514") {
			t.Errorf("256-character subject failed for an unexpected reason: %v", longSubjectErr)
		}
	}

	if err := db.Exec("UPDATE oauth_identities SET deleted_at = now() WHERE id = ?", id).Error; err != nil {
		t.Fatalf("soft-delete first identity: %v", err)
	}
	newActiveID := insertIdentity(userBefore.ID, "google", "Subject-X")
	var activeCount, historicalCount int64
	if err := db.Raw("SELECT count(*) FROM oauth_identities WHERE provider = 'google' AND subject = 'Subject-X' AND deleted_at IS NULL").Scan(&activeCount).Error; err != nil {
		t.Fatalf("query active identity after relinking: %v", err)
	}
	if err := db.Raw("SELECT count(*) FROM oauth_identities WHERE provider = 'google' AND subject = 'Subject-X'").Scan(&historicalCount).Error; err != nil {
		t.Fatalf("query identity history after relinking: %v", err)
	}
	if activeCount != 1 || historicalCount != 2 || newActiveID == id {
		t.Errorf("soft-delete/relink did not retain one old row and one new active row: active=%d history=%d old=%d new=%d", activeCount, historicalCount, id, newActiveID)
	}
	var oldState, deletedState string
	if err := db.Raw(`SELECT old_state::text, new_state::text FROM oauth_identity_audit_log
		WHERE identity_id = ? AND new_state->>'deleted_at' IS NOT NULL ORDER BY id DESC LIMIT 1`, id).Row().Scan(&oldState, &deletedState); err != nil {
		t.Fatalf("read soft-delete audit snapshots: %v", err)
	}
	if !strings.Contains(oldState, `"deleted_at": null`) || !strings.Contains(deletedState, `"deleted_at":`) || strings.Contains(oldState+deletedState, "subject") {
		t.Errorf("soft-delete audit did not preserve the before/after snapshots without subject: old=%s new=%s", oldState, deletedState)
	}
	var oldUpdatedAt time.Time
	if err := db.Raw("SELECT updated_at FROM oauth_identities WHERE id = ?", newActiveID).Row().Scan(&oldUpdatedAt); err != nil {
		t.Fatalf("read new identity updated_at: %v", err)
	}
	if err := db.Exec("UPDATE oauth_identities SET updated_at = now() - interval '1 day' WHERE id = ?", newActiveID).Error; err != nil {
		t.Fatalf("exercise updated_at trigger: %v", err)
	}
	var triggerUpdatedAt time.Time
	if err := db.Raw("SELECT updated_at FROM oauth_identities WHERE id = ?", newActiveID).Row().Scan(&triggerUpdatedAt); err != nil {
		t.Fatalf("read updated_at after direct update: %v", err)
	}
	if !triggerUpdatedAt.After(oldUpdatedAt) {
		t.Errorf("updated_at trigger did not replace a stale caller value: before=%v after=%v", oldUpdatedAt, triggerUpdatedAt)
	}

	assertRejected("delete user with retained identity history", "23503", db.Exec("DELETE FROM users WHERE id = ?", secondUserID).Error)
	assertRejected("change primary key of user with retained identity history", "23503", db.Exec("UPDATE users SET id = id + 100000 WHERE id = ?", secondUserID).Error)

	var actorEmailBefore string
	if err := db.Raw("SELECT email FROM users WHERE id = ?", fifthUserID).Row().Scan(&actorEmailBefore); err != nil {
		t.Fatalf("read actor email: %v", err)
	}
	var actorSnapshotIdentity int64
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin audit actor transaction: %v", tx.Error)
	}
	if err := tx.Raw("SELECT set_config('jelly.actor_id', ?, true)", fmt.Sprint(fifthUserID)).Row().Scan(new(string)); err != nil {
		_ = tx.Rollback().Error
		t.Fatalf("set transaction-local audit actor: %v", err)
	}
	if err := tx.Raw(`INSERT INTO oauth_identities (user_id, provider, subject)
		VALUES (?, 'github', 'audit-subject-secret') RETURNING id`, thirdUserID).Row().Scan(&actorSnapshotIdentity); err != nil {
		_ = tx.Rollback().Error
		t.Fatalf("insert identity with actor: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit actor audit transaction: %v", err)
	}
	if err := db.Exec("UPDATE users SET email = 'renamed@example.test' WHERE id = ?", fifthUserID).Error; err != nil {
		t.Fatalf("change actor email after audited identity write: %v", err)
	}
	var actorIDSnapshot, actorEmailSnapshot string
	var newState string
	if err := db.Raw("SELECT actor_id::text, actor_email, new_state::text FROM oauth_identity_audit_log WHERE identity_id = ? ORDER BY id DESC LIMIT 1", actorSnapshotIdentity).Row().Scan(&actorIDSnapshot, &actorEmailSnapshot, &newState); err != nil {
		t.Fatalf("read audit actor snapshot: %v", err)
	}
	if actorIDSnapshot != fmt.Sprint(fifthUserID) {
		t.Errorf("audit actor id = %q, want writing actor %d", actorIDSnapshot, fifthUserID)
	}
	if actorEmailSnapshot != actorEmailBefore {
		t.Errorf("audit email changed with actor account: got %q, want snapshot %q", actorEmailSnapshot, actorEmailBefore)
	}
	if err := db.Exec("DELETE FROM users WHERE id = ?", fifthUserID).Error; err != nil {
		t.Fatalf("delete actor after audit snapshot: %v", err)
	}
	var actorIDIsNull bool
	var actorEmailAfterDelete string
	if err := db.Raw("SELECT actor_id IS NULL, actor_email FROM oauth_identity_audit_log WHERE identity_id = ? ORDER BY id DESC LIMIT 1", actorSnapshotIdentity).Row().Scan(&actorIDIsNull, &actorEmailAfterDelete); err != nil {
		t.Fatalf("read nullable audit actor after account deletion: %v", err)
	}
	if !actorIDIsNull {
		t.Error("deleting the actor did not preserve audit history with a null actor_id")
	}
	if actorEmailAfterDelete != actorEmailBefore {
		t.Errorf("deleting the actor changed the audit email snapshot: got %q, want %q", actorEmailAfterDelete, actorEmailBefore)
	}
	if !strings.Contains(newState, `"id"`) || !strings.Contains(newState, `"user_id"`) || !strings.Contains(newState, `"provider"`) || strings.Contains(newState, "subject") {
		t.Errorf("audit state does not contain only the expected identity snapshot fields: %s", newState)
	}
	var sensitiveAuditText string
	if err := db.Raw(`SELECT coalesce(string_agg(coalesce(old_state::text, '') || coalesce(new_state::text, '') || coalesce(actor_email, ''), ' '), '')
		FROM oauth_identity_audit_log`).Row().Scan(&sensitiveAuditText); err != nil {
		t.Fatalf("inspect persisted audit snapshots: %v", err)
	}
	if strings.Contains(sensitiveAuditText, "audit-subject-secret") || strings.Contains(strings.ToLower(sensitiveAuditText), "token") || strings.Contains(strings.ToLower(sensitiveAuditText), "code") {
		t.Errorf("audit rows contain OAuth material: %s", sensitiveAuditText)
	}

	var oldAuditID int64
	if err := db.Raw(`INSERT INTO oauth_identity_audit_log
		(identity_id, user_id, provider, action, old_state, new_state, actor_id, actor_email, created_at)
		VALUES (9223372036854770000, 9223372036854770000, 'google', 'INSERT', '{}'::jsonb, '{}'::jsonb, NULL, NULL, now() - interval '91 days') RETURNING id`).Row().Scan(&oldAuditID); err != nil {
		t.Fatalf("seed expired audit row: %v", err)
	}
	retentionIdentity := insertIdentity(fourthUserID, "google", "retention-subject")
	var oldAuditRows, freshAuditRows int64
	if err := db.Raw("SELECT count(*) FROM oauth_identity_audit_log WHERE id = ?", oldAuditID).Scan(&oldAuditRows).Error; err != nil {
		t.Fatalf("check expired audit retention: %v", err)
	}
	if err := db.Raw("SELECT count(*) FROM oauth_identity_audit_log WHERE identity_id = ?", retentionIdentity).Scan(&freshAuditRows).Error; err != nil {
		t.Fatalf("check fresh audit retention: %v", err)
	}
	if oldAuditRows != 0 || freshAuditRows == 0 {
		t.Errorf("audit retention cleanup did not remove only the expired row: expired=%d fresh=%d", oldAuditRows, freshAuditRows)
	}

	var auditsBefore int64
	if err := db.Raw("SELECT count(*) FROM oauth_identity_audit_log").Scan(&auditsBefore).Error; err != nil {
		t.Fatalf("count audit rows before rollback check: %v", err)
	}
	tx = db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin rollback test transaction: %v", tx.Error)
	}
	var rolledBackIdentity int64
	if err := tx.Raw(`INSERT INTO oauth_identities (user_id, provider, subject)
		VALUES (?, 'github', 'rollback-subject') RETURNING id`, fourthUserID).Row().Scan(&rolledBackIdentity); err != nil {
		_ = tx.Rollback().Error
		t.Fatalf("insert identity inside rollback transaction: %v", err)
	}
	var inTransactionAuditRows int64
	if err := tx.Raw("SELECT count(*) FROM oauth_identity_audit_log WHERE identity_id = ?", rolledBackIdentity).Scan(&inTransactionAuditRows).Error; err != nil {
		_ = tx.Rollback().Error
		t.Fatalf("read audit row inside rollback transaction: %v", err)
	}
	if inTransactionAuditRows == 0 {
		_ = tx.Rollback().Error
		t.Fatal("identity insert did not create its audit row inside the same transaction")
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("roll back identity and audit rows: %v", err)
	}
	var persistedIdentityRows, auditsAfter int64
	if err := db.Raw("SELECT count(*) FROM oauth_identities WHERE id = ?", rolledBackIdentity).Scan(&persistedIdentityRows).Error; err != nil {
		t.Fatalf("check rolled-back identity: %v", err)
	}
	if err := db.Raw("SELECT count(*) FROM oauth_identity_audit_log").Scan(&auditsAfter).Error; err != nil {
		t.Fatalf("count audit rows after rollback: %v", err)
	}
	if persistedIdentityRows != 0 || auditsAfter != auditsBefore {
		t.Errorf("rollback left partial identity/audit data: identity rows=%d audit before=%d after=%d", persistedIdentityRows, auditsBefore, auditsAfter)
	}
}

func TestOAuthIdentityMigrationDeleteAudit(t *testing.T) {
	db := newOAuthIdentityMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	var identityID int64
	if err := db.Raw(`INSERT INTO oauth_identities (user_id, provider, subject)
		VALUES (101, 'google', 'delete-audit-subject') RETURNING id`).Row().Scan(&identityID); err != nil {
		t.Fatalf("insert identity to delete: %v", err)
	}
	if err := db.Exec("DELETE FROM oauth_identities WHERE id = ?", identityID).Error; err != nil {
		t.Fatalf("hard-delete own test identity: %v", err)
	}

	var action, oldState string
	var newStateIsNull bool
	if err := db.Raw(`SELECT action, coalesce(old_state::text, ''), new_state IS NULL
		FROM oauth_identity_audit_log WHERE identity_id = ? ORDER BY id DESC LIMIT 1`, identityID).Row().Scan(
		&action, &oldState, &newStateIsNull,
	); err != nil {
		t.Fatalf("read DELETE audit snapshot: %v", err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(oldState), &snapshot); err != nil {
		t.Errorf("DELETE audit old_state is not a JSON snapshot: %q (%v)", oldState, err)
	}
	if action != "DELETE" {
		t.Errorf("audit action = %q, want DELETE", action)
	}
	if len(snapshot) != 4 {
		t.Errorf("DELETE audit old_state must have exactly the four redacted fields, got %s", oldState)
	}
	for _, key := range []string{"id", "user_id", "provider", "deleted_at"} {
		if _, ok := snapshot[key]; !ok {
			t.Errorf("DELETE audit old_state is missing %q: %s", key, oldState)
		}
	}
	if string(snapshot["id"]) != fmt.Sprint(identityID) || string(snapshot["user_id"]) != "101" || string(snapshot["provider"]) != `"google"` || string(snapshot["deleted_at"]) != "null" {
		t.Errorf("DELETE audit old_state values do not match the deleted identity: %s", oldState)
	}
	if !newStateIsNull {
		t.Errorf("DELETE audit new_state must be SQL NULL, got %q", oldState)
	}
	if strings.Contains(oldState, "subject") || strings.Contains(oldState, "delete-audit-subject") {
		t.Errorf("DELETE audit snapshot exposed the subject: %s", oldState)
	}
	var remaining int64
	if err := db.Raw("SELECT count(*) FROM oauth_identities WHERE id = ?", identityID).Scan(&remaining).Error; err != nil {
		t.Fatalf("confirm hard-deleted identity row: %v", err)
	}
	if remaining != 0 {
		t.Errorf("identity row remains after DELETE: count=%d", remaining)
	}
}

func TestOAuthIdentityMigrationContextCancellation(t *testing.T) {
	db := newOAuthIdentityMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("prepare migrated database: %v", err)
	}

	lockTx := db.Begin()
	if lockTx.Error != nil {
		t.Fatalf("begin table-lock transaction: %v", lockTx.Error)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lockTx.Rollback().Error
		}
	}()
	var blockerPID int
	if err := lockTx.Raw("SELECT pg_backend_pid()").Row().Scan(&blockerPID); err != nil {
		t.Fatalf("read table-lock backend PID: %v", err)
	}
	if err := lockTx.Exec("LOCK TABLE public.oauth_identities IN ACCESS EXCLUSIVE MODE").Error; err != nil {
		t.Fatalf("lock own oauth_identities table: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	migrationDone := make(chan error, 1)
	go func() {
		migrationDone <- MigrateDB(db.WithContext(ctx))
	}()

	blocked, lastQuery, waitErr := waitForOAuthIdentityMigrationLock(t, db, blockerPID, 3*time.Second)
	cancel()
	var migrationErr error
	returnedBeforeUnlock := false
	if blocked {
		select {
		case migrationErr = <-migrationDone:
			returnedBeforeUnlock = true
		case <-time.After(750 * time.Millisecond):
		}
	}
	unlockErr := lockTx.Rollback().Error
	if unlockErr == nil {
		lockHeld = false
	}
	if unlockErr != nil {
		t.Errorf("release own oauth_identities table lock: %v", unlockErr)
	}
	if !returnedBeforeUnlock {
		select {
		case migrationErr = <-migrationDone:
		case <-time.After(10 * time.Second):
			t.Errorf("MigrateDB goroutine did not finish after releasing the table lock")
		}
	}
	if waitErr != nil {
		t.Errorf("observe migration waiting on oauth_identities relation lock: %v (last query: %q)", waitErr, lastQuery)
	}
	if !blocked {
		t.Errorf("did not observe MigrateDB waiting on the locked oauth_identities relation; last query: %q", lastQuery)
	}
	if blocked && !returnedBeforeUnlock {
		t.Errorf("MigrateDB did not return before the table lock was released after its context was canceled")
	}
	if returnedBeforeUnlock && migrationErr == nil {
		t.Error("MigrateDB returned nil after cancellation while its DDL was blocked")
	}
}

func waitForOAuthIdentityMigrationLock(t *testing.T, db *gorm.DB, blockerPID int, timeout time.Duration) (bool, string, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastQuery string
	for time.Now().Before(deadline) {
		var waiting bool
		var query string
		err := db.Raw(`SELECT activity.query
			FROM pg_stat_activity AS activity
			JOIN pg_locks AS relation_lock ON relation_lock.pid = activity.pid
			WHERE activity.datname = current_database()
			  AND activity.pid <> ?
			  AND activity.pid <> pg_backend_pid()
			  AND activity.wait_event_type = 'Lock'
			  AND activity.query ILIKE '%oauth_identities%'
			  AND relation_lock.locktype = 'relation'
			  AND relation_lock.relation = 'public.oauth_identities'::regclass
			  AND NOT relation_lock.granted
			LIMIT 1`, blockerPID).Row().Scan(&query)
		if err == nil {
			lastQuery = query
			waiting = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, lastQuery, err
		}
		if waiting {
			return true, lastQuery, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false, lastQuery, nil
}

type oauthLegacyUserSnapshot struct {
	ID                                                   int64
	Username, Email, Password, AvatarURL, Role, Provider string
	CreatedAtEpoch, UpdatedAtEpoch                       string
}

type oauthLegacyPostSnapshot struct {
	ID, AuthorID                                               int64
	Title, Slug, Content, PostType, Status, Template           string
	MetaJSON, PublishedAtEpoch, CreatedAtEpoch, UpdatedAtEpoch string
}

func readOAuthLegacyRows(t *testing.T, db *gorm.DB) (oauthLegacyUserSnapshot, oauthLegacyPostSnapshot) {
	t.Helper()
	var user oauthLegacyUserSnapshot
	if err := db.Raw(`SELECT id, username::text, email::text, password::text, avatar_url::text, role::text, provider::text,
		extract(epoch FROM created_at)::text, extract(epoch FROM updated_at)::text
		FROM users WHERE id = 101`).Row().Scan(
		&user.ID, &user.Username, &user.Email, &user.Password, &user.AvatarURL,
		&user.Role, &user.Provider, &user.CreatedAtEpoch, &user.UpdatedAtEpoch,
	); err != nil {
		t.Fatalf("read complete legacy User snapshot: %v", err)
	}
	var post oauthLegacyPostSnapshot
	if err := db.Raw(`SELECT id, title::text, slug::text, content::text, type::text, status::text, author_id, template::text,
		coalesce(meta::jsonb::text, 'null'), coalesce(extract(epoch FROM published_at)::text, '<NULL>'),
		extract(epoch FROM created_at)::text, extract(epoch FROM updated_at)::text
		FROM posts WHERE id = 202`).Row().Scan(
		&post.ID, &post.Title, &post.Slug, &post.Content, &post.PostType, &post.Status,
		&post.AuthorID, &post.Template, &post.MetaJSON, &post.PublishedAtEpoch,
		&post.CreatedAtEpoch, &post.UpdatedAtEpoch,
	); err != nil {
		t.Fatalf("read complete legacy Post snapshot: %v", err)
	}
	return user, post
}

func newOAuthIdentityMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run OAuth identity migration tests against the dedicated PostgreSQL container")
	}

	admin, err := Open(config.DatabaseConfig{
		Host: oauthMigrationTestHost, Port: oauthMigrationTestPort, User: oauthMigrationTestUser,
		Password: oauthMigrationTestPass, Name: oauthMigrationTestAdminDB, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get dedicated PostgreSQL admin pool: %v", err)
	}
	t.Cleanup(func() {
		if err := adminSQL.Close(); err != nil {
			t.Errorf("close dedicated PostgreSQL admin pool: %v", err)
		}
	})

	dbName := fmt.Sprintf("%s%d_%d", oauthMigrationTestPrefix, os.Getpid(), oauthMigrationTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated OAuth migration database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, oauthMigrationTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated OAuth migration database %s: %v", dbName, err)
		}
	})

	db, err := Open(config.DatabaseConfig{
		Host: oauthMigrationTestHost, Port: oauthMigrationTestPort, User: oauthMigrationTestUser,
		Password: oauthMigrationTestPass, Name: dbName, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("open isolated OAuth migration database %s: %v", dbName, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated OAuth migration pool for %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close isolated OAuth migration database %s: %v", dbName, err)
		}
	})

	createLegacySchema := []string{
		`CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY, username VARCHAR(50), email VARCHAR(255),
			password VARCHAR(255), avatar_url VARCHAR(255), role VARCHAR(255) DEFAULT 'editor',
			provider VARCHAR(255), created_at TIMESTAMP, updated_at TIMESTAMP
		)`,
		`CREATE TABLE posts (
			id BIGSERIAL PRIMARY KEY, title VARCHAR(255), slug VARCHAR(255), content TEXT,
			type VARCHAR(255) DEFAULT 'post', status VARCHAR(255) DEFAULT 'draft', author_id BIGINT,
			template VARCHAR(255), meta JSONB, published_at TIMESTAMP, created_at TIMESTAMP, updated_at TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX uni_users_username ON users (username)`,
		`CREATE UNIQUE INDEX uni_users_email ON users (email)`,
		`CREATE UNIQUE INDEX uni_posts_slug ON posts (slug)`,
		`INSERT INTO users (id, username, email, password, avatar_url, role, provider, created_at, updated_at)
			VALUES (101, 'legacy-author', 'legacy@example.test', 'legacy-hash', '', 'author', 'google', now(), now())`,
		`INSERT INTO posts (id, title, slug, content, type, status, author_id, template, meta, created_at, updated_at)
			VALUES (202, 'Legacy title', 'legacy-title', 'Legacy body', 'post', 'published', 101, '', '{}'::jsonb, now(), now())`,
	}
	for _, statement := range createLegacySchema {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("prepare pre-migration User/Post data in %s: %v", dbName, err)
		}
	}
	return db
}
