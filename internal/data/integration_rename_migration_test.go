package data

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Acceptance test for migration 0027_integration_rename.sql
// (docs/tickets/25-integration-internal-rename.md, 25a 內部). The migration
// renames the agent tables, columns, constraints, indexes, trigger, function
// and sequences to integration, rewrites the audit history, widens
// integration_events.actor_type and drops api_tokens.
//
// The test builds the pre-rename schema at version 25, seeds agent rows and
// their audit rows, then runs 0026 and 0027 and checks every point of the 25a
// acceptance contract.

const integrationRenameMigrationPath = "migrations/0027_integration_rename.sql"

func irnSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// irnRegclass reports whether name resolves to a relation.
func irnRegclass(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.Raw("SELECT to_regclass(?) IS NOT NULL", name).Row().Scan(&exists); err != nil {
		t.Fatalf("check to_regclass(%q): %v", name, err)
	}
	return exists
}

// irnColumn reports whether table has column in the current schema.
func irnColumn(t *testing.T, db *gorm.DB, table, column string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, column).Row().Scan(&n); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	return n > 0
}

func irnCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// irnField reads one system_logs.fields value and whether the key exists.
func irnField(t *testing.T, db *gorm.DB, logID int64, key string) (string, bool) {
	t.Helper()
	var value string
	var exists bool
	if err := db.Raw(`SELECT coalesce(fields->>?, ''), jsonb_exists(fields, ?) FROM system_logs WHERE id = ?`,
		key, key, logID).Row().Scan(&value, &exists); err != nil {
		t.Fatalf("read system_logs.fields[%q] for log %d: %v", key, logID, err)
	}
	return value, exists
}

type irnEvent struct {
	ActorType       string
	Module          string
	Action          string
	Route           string
	IntegrationID   int64
	IntegrationName string
}

func TestIntegrationRenameMigrationRenamesEveryAgentObject(t *testing.T) {
	sqlText, err := embeddedMigrationFiles.ReadFile(integrationRenameMigrationPath)
	if err != nil {
		t.Fatalf("migration 0027_integration_rename.sql is missing from internal/data/migrations (ticket 25a requires it): %v", err)
	}

	db := newAccountsMigrationDB(t)
	if err := migrateDBThrough(db, 25); err != nil {
		t.Fatalf("migrateDBThrough(db, 25): %v", err)
	}

	owner := core.User{Username: "integration-rename-owner", Email: "integration-rename@example.test", Role: "admin", Provider: "local"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}

	t0 := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	const (
		integrationName    = "planner"
		integrationDesc    = "planner 的說明"
		integrationPerms   = "posts.read,posts.write"
		integrationRoleCap = "editor"
		legacyPrefix       = "jla_fixture1"
	)
	legacyHash := irnSHA256Hex("jla_fixture_key_0001")
	tokenHash := irnSHA256Hex("jly_fixture_token_0002")

	var integrationID int64
	// role_cap arrives with 0026, so it is set after that migration below.
	if err := db.Raw(`INSERT INTO agents (name, description, permissions, owner_id, created_by, status, created_at, updated_at)
		VALUES (?, ?, '{posts.read,posts.write}'::text[], ?, ?, 'active', ?, ?) RETURNING id`,
		integrationName, integrationDesc, owner.ID, owner.ID, t0, t0).Row().Scan(&integrationID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	var keyID int64
	if err := db.Raw(`INSERT INTO agent_keys (agent_id, key_hash, prefix, created_by, created_at)
		VALUES (?, ?, ?, ?, ?) RETURNING id`,
		integrationID, legacyHash, legacyPrefix, owner.ID, t0).Row().Scan(&keyID); err != nil {
		t.Fatalf("seed agent key: %v", err)
	}
	if err := db.Exec(`INSERT INTO api_tokens (user_id, name, note, token_hash, last4, scopes, created_at)
		VALUES (?, 'planner token', 'from token', ?, '0002', '{author}', ?)`,
		owner.ID, tokenHash, t0).Error; err != nil {
		t.Fatalf("seed api token: %v", err)
	}

	var agentEventID, userEventID int64
	if err := db.Raw(`INSERT INTO agent_events (at, actor_type, agent_id, agent_name, key_id, key_prefix, action, module, method, route, status)
		VALUES (?, 'agent', ?, ?, ?, ?, 'agent.create', 'agents', 'POST', '/api/admin/agents/1', 201) RETURNING id`,
		t0, integrationID, integrationName, keyID, legacyPrefix).Row().Scan(&agentEventID); err != nil {
		t.Fatalf("seed agent-actor event: %v", err)
	}
	if err := db.Raw(`INSERT INTO agent_events (at, actor_type, actor_user_id, agent_id, agent_name, action, module, method, route, status)
		VALUES (?, 'user', ?, ?, ?, 'agent.create', 'agents', 'GET', '/api/agent/v1/me', 200) RETURNING id`,
		t0, owner.ID, integrationID, integrationName).Row().Scan(&userEventID); err != nil {
		t.Fatalf("seed user-actor event: %v", err)
	}

	agentField := strconv.FormatInt(integrationID, 10)
	insertLog := func(source, message, fields string) int64 {
		var id int64
		if err := db.Raw(`INSERT INTO system_logs (level, source, message, fields)
			VALUES (1, ?, ?, CAST(? AS JSONB)) RETURNING id`, source, message, fields).Row().Scan(&id); err != nil {
			t.Fatalf("seed system_logs %q: %v", message, err)
		}
		return id
	}
	logAuditCreate := insertLog("audit", "agent.create", `{"agent_id":`+agentField+`,"module":"agents","route":"/api/admin/agents/1"}`)
	logApp := insertLog("app", "agent sync finished", `{"agent_id":`+agentField+`,"module":"agents","route":"/api/agent/v1/me"}`)
	logAuditOther := insertLog("audit", "system.cleanup", `{"agent_id":`+agentField+`,"module":"agents","route":"/api/agent/v1/me"}`)
	logAuditUnrelatedModule := insertLog("audit", "agent.delete", `{"agent_id":`+agentField+`,"module":"posts","route":"/api/v1/posts/9"}`)

	if err := migrateDBThrough(db, 26); err != nil {
		t.Fatalf("migrateDBThrough(db, 26): %v", err)
	}
	if err := db.Exec(`UPDATE agents SET role_cap = ? WHERE id = ?`, integrationRoleCap, integrationID).Error; err != nil {
		t.Fatalf("set role_cap after 0026: %v", err)
	}
	if err := migrateDBThrough(db, 27); err != nil {
		t.Fatalf("migrateDBThrough(db, 27): %v", err)
	}

	// New tables exist; every old table and api_tokens is gone.
	for _, table := range []string{"integrations", "integration_keys", "integration_events"} {
		if !irnRegclass(t, db, table) {
			t.Errorf("table %s is missing after 0027", table)
		}
	}
	for _, table := range []string{"agents", "agent_keys", "agent_events", "api_tokens"} {
		if irnRegclass(t, db, table) {
			t.Errorf("table %s still exists after 0027", table)
		}
	}

	// New columns exist; old columns are gone.
	for _, col := range []struct{ table, column string }{
		{"integration_keys", "integration_id"},
		{"integration_events", "integration_id"},
		{"integration_events", "integration_name"},
		{"integrations", "role_cap"},
	} {
		if !irnColumn(t, db, col.table, col.column) {
			t.Errorf("%s.%s is missing after 0027", col.table, col.column)
		}
	}
	for _, col := range []struct{ table, column string }{
		{"integration_keys", "agent_id"},
		{"integration_events", "agent_id"},
		{"integration_events", "agent_name"},
	} {
		if irnColumn(t, db, col.table, col.column) {
			t.Errorf("%s.%s still exists after 0027", col.table, col.column)
		}
	}

	// No user-visible object keeps the agent name.
	for _, object := range []struct{ label, query string }{
		{"pg_class relation", `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
			AND c.relkind IN ('r', 'i', 'S') AND lower(c.relname) LIKE '%agent%'`},
		{"pg_constraint constraint", `SELECT count(*) FROM pg_constraint
			WHERE connamespace = 'public'::regnamespace AND lower(conname) LIKE '%agent%'`},
		{"pg_trigger trigger", `SELECT count(*) FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE NOT t.tgisinternal AND n.nspname = 'public' AND lower(t.tgname) LIKE '%agent%'`},
		{"pg_proc function", `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'public' AND lower(p.proname) LIKE '%agent%'`},
	} {
		if n := irnCount(t, db, object.query); n != 0 {
			t.Errorf("%s still has %d object(s) whose name contains agent", object.label, n)
		}
	}

	// The three IDENTITY sequences carry the new names.
	for _, seq := range []struct{ table, want string }{
		{"integrations", "public.integrations_id_seq"},
		{"integration_keys", "public.integration_keys_id_seq"},
		{"integration_events", "public.integration_events_id_seq"},
	} {
		var got sql.NullString
		if err := db.Raw("SELECT pg_get_serial_sequence(?, 'id')", seq.table).Row().Scan(&got); err != nil {
			t.Fatalf("read serial sequence of %s.id: %v", seq.table, err)
		}
		if !got.Valid || got.String != seq.want {
			t.Errorf("pg_get_serial_sequence(%q, 'id') = %v, want %q", seq.table, got, seq.want)
		}
	}

	// actor_type is VARCHAR(16) and accepts only user and integration.
	var actorType string
	var actorTypeLen sql.NullInt64
	if err := db.Raw(`SELECT data_type, character_maximum_length FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'integration_events' AND column_name = 'actor_type'`).
		Row().Scan(&actorType, &actorTypeLen); err != nil {
		t.Fatalf("read integration_events.actor_type type: %v", err)
	}
	if actorType != "character varying" || !actorTypeLen.Valid || actorTypeLen.Int64 != 16 {
		t.Errorf("integration_events.actor_type is %s(%v), want character varying(16)", actorType, actorTypeLen)
	}
	for _, actor := range []string{"user", "integration", "agent", "admin"} {
		err := db.Exec(`INSERT INTO integration_events (actor_type, integration_id, integration_name, action, module)
			VALUES (?, ?, 'probe', 'integration.probe', 'integrations')`, actor, integrationID).Error
		wantOK := actor == "user" || actor == "integration"
		if wantOK && err != nil {
			t.Errorf("integration_events.actor_type rejected %q: %v", actor, err)
		}
		if !wantOK && err == nil {
			t.Errorf("integration_events.actor_type accepted %q, want the check to allow only user and integration", actor)
		}
	}

	// The historical event rows carry the new values.
	readEvent := func(id int64) irnEvent {
		t.Helper()
		var ev irnEvent
		if err := db.Raw(`SELECT actor_type, module, action, route, integration_id, integration_name
			FROM integration_events WHERE id = ?`, id).Row().Scan(
			&ev.ActorType, &ev.Module, &ev.Action, &ev.Route, &ev.IntegrationID, &ev.IntegrationName); err != nil {
			t.Fatalf("read integration_events %d: %v", id, err)
		}
		return ev
	}
	agentEvent := readEvent(agentEventID)
	if agentEvent.ActorType != "integration" || agentEvent.Module != "integrations" ||
		agentEvent.Action != "integration.create" || agentEvent.Route != "/api/admin/integrations/1" ||
		agentEvent.IntegrationID != integrationID || agentEvent.IntegrationName != integrationName {
		t.Errorf("agent-actor event after 0027 = %+v, want actor integration, module integrations, action integration.create, route /api/admin/integrations/1, integration %d %q",
			agentEvent, integrationID, integrationName)
	}
	userEvent := readEvent(userEventID)
	if userEvent.ActorType != "user" || userEvent.Module != "integrations" ||
		userEvent.Action != "integration.create" || userEvent.Route != "/api/v1/me" ||
		userEvent.IntegrationID != integrationID || userEvent.IntegrationName != integrationName {
		t.Errorf("user-actor event after 0027 = %+v, want actor user, module integrations, action integration.create, route /api/v1/me, integration %d %q",
			userEvent, integrationID, integrationName)
	}

	// system_logs rewrites only the agent.* audit messages, renames the
	// agent_id field on every row, and rewrites module and route values by the
	// same prefix rules. Rows that must not change stay unchanged.
	assertLog := func(label string, id int64, wantMessage string) {
		t.Helper()
		var message string
		if err := db.Raw("SELECT message FROM system_logs WHERE id = ?", id).Row().Scan(&message); err != nil {
			t.Fatalf("%s: read system_logs %d: %v", label, id, err)
		}
		if message != wantMessage {
			t.Errorf("%s: system_logs.message = %q, want %q", label, message, wantMessage)
		}
		if _, ok := irnField(t, db, id, "agent_id"); ok {
			t.Errorf("%s: system_logs.fields still has agent_id", label)
		}
		if value, ok := irnField(t, db, id, "integration_id"); !ok || value != agentField {
			t.Errorf("%s: system_logs.fields.integration_id = %q (exists=%v), want %q", label, value, ok, agentField)
		}
	}
	assertLog("audit agent.create", logAuditCreate, "integration.create")
	assertLog("app row", logApp, "agent sync finished")
	assertLog("audit non-agent message", logAuditOther, "system.cleanup")
	assertLog("audit agent.delete", logAuditUnrelatedModule, "integration.delete")

	assertField := func(label string, id int64, key, want string) {
		t.Helper()
		if value, ok := irnField(t, db, id, key); !ok || value != want {
			t.Errorf("%s: system_logs.fields.%s = %q (exists=%v), want %q", label, key, value, ok, want)
		}
	}
	assertField("audit agent.create module", logAuditCreate, "module", "integrations")
	assertField("audit agent.create route", logAuditCreate, "route", "/api/admin/integrations/1")
	assertField("app module", logApp, "module", "integrations")
	assertField("app route", logApp, "route", "/api/v1/me")
	assertField("audit non-agent module", logAuditOther, "module", "integrations")
	assertField("audit non-agent route", logAuditOther, "route", "/api/v1/me")
	assertField("audit unrelated module", logAuditUnrelatedModule, "module", "posts")
	assertField("audit unrelated route", logAuditUnrelatedModule, "route", "/api/v1/posts/9")

	// The existing integration row keeps its data.
	var gotName, gotDescription, gotPermissions string
	var gotRoleCap sql.NullString
	if err := db.Raw(`SELECT name, description, array_to_string(permissions, ','), role_cap
		FROM integrations WHERE id = ?`, integrationID).Row().Scan(&gotName, &gotDescription, &gotPermissions, &gotRoleCap); err != nil {
		t.Fatalf("read integration %d after 0027: %v", integrationID, err)
	}
	if gotName != integrationName || gotDescription != integrationDesc || gotPermissions != integrationPerms {
		t.Errorf("integration %d changed: name=%q description=%q permissions=%q, want %q %q %q",
			integrationID, gotName, gotDescription, gotPermissions, integrationName, integrationDesc, integrationPerms)
	}
	if !gotRoleCap.Valid || gotRoleCap.String != integrationRoleCap {
		t.Errorf("integration %d role_cap = %v, want %q", integrationID, gotRoleCap, integrationRoleCap)
	}
	var gotHash, gotPrefix string
	if err := db.Raw(`SELECT key_hash, prefix FROM integration_keys WHERE integration_id = ? AND key_hash = ?`,
		integrationID, legacyHash).Row().Scan(&gotHash, &gotPrefix); err != nil {
		t.Fatalf("read the migrated legacy key: %v", err)
	}
	if gotHash != legacyHash || gotPrefix != legacyPrefix {
		t.Errorf("legacy key after 0027 = hash %q prefix %q, want hash %q prefix %q", gotHash, gotPrefix, legacyHash, legacyPrefix)
	}

	// Both legacy jla_ and new jly_ prefixes stay valid for new key rows.
	for _, probe := range []struct{ prefix, raw string }{
		{"jla_abcd1234", "jla_probe_after_rename_0003"},
		{"jly_abcd", "jly_probe_after_rename_0004"},
	} {
		if err := db.Exec(`INSERT INTO integration_keys (integration_id, key_hash, prefix) VALUES (?, ?, ?)`,
			integrationID, irnSHA256Hex(probe.raw), probe.prefix).Error; err != nil {
			t.Errorf("integration_keys rejected prefix %q after 0027: %v", probe.prefix, err)
		}
	}

	// MigrateDB and a direct rerun of 0027 are both idempotent.
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB after 0027 is not idempotent: %v", err)
	}
	if err := db.Exec(string(sqlText)).Error; err != nil {
		t.Fatalf("rerunning 0027_integration_rename.sql is not idempotent: %v", err)
	}
	if irnRegclass(t, db, "agents") || irnRegclass(t, db, "api_tokens") {
		t.Errorf("rerunning 0027 brought back a legacy table")
	}
	if n := irnCount(t, db, `SELECT count(*) FROM integration_keys WHERE integration_id = ? AND prefix IN ('jla_abcd1234', 'jly_abcd')`, integrationID); n != 2 {
		t.Errorf("after rerunning 0027 the integration has %d probe keys, want 2", n)
	}
	if n := irnCount(t, db, `SELECT count(*) FROM integration_keys WHERE key_hash = ? AND prefix = 'jly_0002'`, tokenHash); n != 1 {
		t.Errorf("after rerunning 0027 the token-derived jly_ key count = %d, want 1", n)
	}
	var rerunName string
	if err := db.Raw("SELECT name FROM integrations WHERE id = ?", integrationID).Row().Scan(&rerunName); err != nil || rerunName != integrationName {
		t.Errorf("after rerunning 0027 integration %d name = %q (err=%v), want %q", integrationID, rerunName, err, integrationName)
	}
}
