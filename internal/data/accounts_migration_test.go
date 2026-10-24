package data

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Acceptance tests for the account migrations 0002 to 0007
// (docs/tickets/06-accounts.md, D1, 資料表). Each test gets its own fresh
// database on the dedicated PostgreSQL container.

const (
	accountsMigrationTestHost    = "127.0.0.1"
	accountsMigrationTestPort    = "55432"
	accountsMigrationTestUser    = "jelly_test"
	accountsMigrationTestPass    = "jelly-local-acceptance"
	accountsMigrationTestAdminDB = "jelly_acceptance"
	accountsMigrationTestPrefix  = "jelly_accounts_migration_"
)

var accountsMigrationTestDBCounter atomic.Int64

func TestAccountsMigrationIdempotentRerun(t *testing.T) {
	db := newAccountsMigrationDB(t)

	if err := MigrateDB(db); err != nil {
		t.Fatalf("first MigrateDB on a fresh database: %v", err)
	}
	u := core.User{Username: "accounts-rerun-user", Email: "accounts-rerun@example.test", Provider: "local"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user between runs: %v", err)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("second MigrateDB returned an error: %v", err)
	}

	for _, table := range []string{"login_sessions", "api_tokens", "notifications", "notification_settings", "notification_prefs", "notification_deliveries", "user_totp", "user_recovery_codes", "user_invites"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s is missing after MigrateDB", table)
		}
	}
	for _, column := range []string{"display_name", "bio", "locale", "ui_theme", "status", "disabled_at", "last_login_at", "password_changed_at", "email_verified_at", "pending_email", "pending_email_hash", "pending_email_expires_at"} {
		if !db.Migrator().HasColumn(&core.User{}, column) {
			t.Errorf("users is missing column %s", column)
		}
	}

	var kept core.User
	if err := db.First(&kept, u.ID).Error; err != nil {
		t.Fatalf("reload user after rerun: %v", err)
	}
	if kept.Username != u.Username || kept.Email != u.Email || kept.Status != "active" || kept.Locale != "zh-TW" || kept.UITheme != "system" {
		t.Errorf("rerun changed an existing user row: %+v", kept)
	}
}

func TestAccountsMigrationRerunPreservesAccountSecurityState(t *testing.T) {
	db := newAccountsMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("first MigrateDB on a fresh database: %v", err)
	}
	u := core.User{Username: "accounts-preserve-user", Email: "accounts-preserve@example.test", Provider: "local"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	disabledAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	pendingExpiry := disabledAt.Add(48 * time.Hour)
	if err := db.Exec(`UPDATE users SET status = 'disabled', disabled_at = ?, pending_email = ?,
		pending_email_hash = ?, pending_email_expires_at = ? WHERE id = ?`,
		disabledAt, "pending@example.test", strings.Repeat("a", 64), pendingExpiry, u.ID).Error; err != nil {
		t.Fatalf("seed account state: %v", err)
	}
	revokedAt := disabledAt.Add(time.Hour)
	if err := db.Exec(`INSERT INTO api_tokens (user_id, name, token_hash, last4, scopes, revoked_at)
		VALUES (?, 'preserve revoked token', ?, 'wxyz', '{author}', ?)`, u.ID, strings.Repeat("b", 64), revokedAt).Error; err != nil {
		t.Fatalf("seed revoked API token: %v", err)
	}
	usedAt := disabledAt.Add(2 * time.Hour)
	if err := db.Exec(`INSERT INTO user_recovery_codes (user_id, code_hash, used_at) VALUES (?, ?, ?)`,
		u.ID, strings.Repeat("c", 64), usedAt).Error; err != nil {
		t.Fatalf("seed used recovery code: %v", err)
	}

	if err := MigrateDB(db); err != nil {
		t.Fatalf("second MigrateDB: %v", err)
	}
	var got struct {
		Status                string
		DisabledAt            time.Time
		PendingEmail          string
		PendingEmailHash      string
		PendingEmailExpiresAt time.Time
	}
	if err := db.Raw(`SELECT status, disabled_at, pending_email, pending_email_hash, pending_email_expires_at
		FROM users WHERE id = ?`, u.ID).Scan(&got).Error; err != nil {
		t.Fatalf("reload account security state: %v", err)
	}
	if got.Status != "disabled" || !got.DisabledAt.Equal(disabledAt) || got.PendingEmail != "pending@example.test" ||
		got.PendingEmailHash != strings.Repeat("a", 64) || !got.PendingEmailExpiresAt.Equal(pendingExpiry) {
		t.Errorf("migration changed account security state: %+v", got)
	}
	var tokenRevokedAt, recoveryUsedAt time.Time
	if err := db.Raw("SELECT revoked_at FROM api_tokens WHERE user_id = ? AND name = ?", u.ID, "preserve revoked token").Scan(&tokenRevokedAt).Error; err != nil {
		t.Fatalf("reload revoked token timestamp: %v", err)
	}
	if err := db.Raw("SELECT used_at FROM user_recovery_codes WHERE user_id = ? AND code_hash = ?", u.ID, strings.Repeat("c", 64)).Scan(&recoveryUsedAt).Error; err != nil {
		t.Fatalf("reload recovery-code timestamp: %v", err)
	}
	if !tokenRevokedAt.Equal(revokedAt) {
		t.Errorf("revoked API token timestamp = %v, want %v", tokenRevokedAt, revokedAt)
	}
	if !recoveryUsedAt.Equal(usedAt) {
		t.Errorf("used recovery code timestamp = %v, want %v", recoveryUsedAt, usedAt)
	}
}

func TestAccountsMigrationConstraints(t *testing.T) {
	db := newAccountsMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}

	u := core.User{Username: "accounts-constraint-user", Email: "accounts-constraint@example.test", Provider: "local"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := db.Exec("UPDATE users SET status = 'weird' WHERE id = ?", u.ID).Error; err == nil {
		t.Errorf("users.status accepted 'weird'; want the active/invited/disabled check to reject it")
	}

	var role string
	if err := db.Raw("SELECT role FROM users WHERE id = ?", u.ID).Row().Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != "author" {
		t.Errorf("role of a user created without a role = %q, want author", role)
	}
	var defaultRole string
	if err := db.Raw("SELECT column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'role'").Row().Scan(&defaultRole); err != nil {
		t.Fatalf("read users.role default: %v", err)
	}
	if !strings.Contains(defaultRole, "author") {
		t.Errorf("users.role column default = %q, want 'author'", defaultRole)
	}

	hash := strings.Repeat("a", 64)
	if err := db.Exec("INSERT INTO api_tokens (user_id, name, token_hash, last4, scopes) VALUES (?, 'first', ?, 'abcd', '{}')", u.ID, hash).Error; err != nil {
		t.Fatalf("insert first api token: %v", err)
	}
	if err := db.Exec("INSERT INTO api_tokens (user_id, name, token_hash, last4, scopes) VALUES (?, 'second', ?, 'wxyz', '{}')", u.ID, hash).Error; err == nil {
		t.Errorf("api_tokens accepted a duplicate token_hash; want a unique constraint")
	}

	var partial int64
	if err := db.Raw(`SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = 'notifications'
		AND indexdef ILIKE '%(user_id%' AND indexdef ILIKE '%read_at IS NULL%'`).Row().Scan(&partial); err != nil {
		t.Fatalf("read pg_indexes: %v", err)
	}
	if partial < 1 {
		t.Errorf("no partial index on notifications(user_id) WHERE read_at IS NULL exists in pg_indexes")
	}
}

func TestAccountsMigrationUserDeleteCascadesPersonalTables(t *testing.T) {
	db := newAccountsMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB: %v", err)
	}

	u := core.User{Username: "accounts-cascade-user", Email: "accounts-cascade@example.test", Provider: "local"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if err := db.Exec(query, args...).Error; err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	exec(`INSERT INTO login_sessions (id, user_id, expires_at) VALUES (?, ?, now() + interval '1 day')`, strings.Repeat("c", 64), u.ID)
	exec(`INSERT INTO api_tokens (user_id, name, token_hash, last4, scopes) VALUES (?, 'cascade', ?, 'wxyz', '{}')`, u.ID, strings.Repeat("d", 64))
	exec(`INSERT INTO user_totp (user_id, secret_enc) VALUES (?, ?)`, u.ID, []byte{1, 2, 3})
	exec(`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (?, ?)`, u.ID, strings.Repeat("e", 64))
	exec(`INSERT INTO user_invites (user_id, kind, token_hash, expires_at) VALUES (?, 'invite', ?, now() + interval '7 days')`, u.ID, strings.Repeat("f", 64))
	exec(`INSERT INTO notification_settings (user_id) VALUES (?)`, u.ID)
	exec(`INSERT INTO notification_prefs (user_id, event) VALUES (?, 'comment.pending')`, u.ID)

	var notificationID int64
	if err := db.Raw(`INSERT INTO notifications (user_id, kind, category) VALUES (?, 'system.update_available', 'system') RETURNING id`, u.ID).Row().Scan(&notificationID); err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	exec(`INSERT INTO notification_deliveries (notification_id, channel) VALUES (?, 'email')`, notificationID)

	personal := []string{"login_sessions", "api_tokens", "user_totp", "user_recovery_codes", "user_invites", "notification_settings", "notification_prefs", "notifications"}
	for _, table := range personal {
		if n := accountsMigrationCount(t, db, fmt.Sprintf("SELECT count(*) FROM %s WHERE user_id = ?", table), u.ID); n != 1 {
			t.Fatalf("precondition: %s has %d rows for the user, want 1", table, n)
		}
	}
	if n := accountsMigrationCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE notification_id = ?", notificationID); n != 1 {
		t.Fatalf("precondition: notification_deliveries has %d rows, want 1", n)
	}

	exec(`DELETE FROM users WHERE id = ?`, u.ID)

	for _, table := range personal {
		if n := accountsMigrationCount(t, db, fmt.Sprintf("SELECT count(*) FROM %s WHERE user_id = ?", table), u.ID); n != 0 {
			t.Errorf("deleting the user left %d rows in %s", n, table)
		}
	}
	if n := accountsMigrationCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE notification_id = ?", notificationID); n != 0 {
		t.Errorf("deleting the user left %d notification_deliveries rows", n)
	}
}

func accountsMigrationCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// newAccountsMigrationDB creates an empty database for one test and drops it
// afterwards. It skips unless JELLY_TEST_POSTGRES=1.
func newAccountsMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run account migration tests against the dedicated PostgreSQL container")
	}

	admin, err := Open(config.DatabaseConfig{
		Host: accountsMigrationTestHost, Port: accountsMigrationTestPort, User: accountsMigrationTestUser,
		Password: accountsMigrationTestPass, Name: accountsMigrationTestAdminDB, Timezone: "UTC",
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

	dbName := fmt.Sprintf("%s%d_%d", accountsMigrationTestPrefix, os.Getpid(), accountsMigrationTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated account migration database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, accountsMigrationTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated account migration database %s: %v", dbName, err)
		}
	})

	db, err := Open(config.DatabaseConfig{
		Host: accountsMigrationTestHost, Port: accountsMigrationTestPort, User: accountsMigrationTestUser,
		Password: accountsMigrationTestPass, Name: dbName, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("open isolated account migration database %s: %v", dbName, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated account migration pool for %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close isolated account migration database %s: %v", dbName, err)
		}
	})
	return db
}
