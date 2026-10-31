package data

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Acceptance tests for migration 0026_tokens_into_keys.sql
// (docs/tickets/22-api-keys-consolidation.md, 資料：遷移 0026). The migration
// turns every personal API token in api_tokens into one agent and one agent
// key. Each test migrates a fresh database, seeds api_tokens rows, and then
// runs the 0026 SQL from the embedded migrations.

const tokensIntoKeysMigrationPath = "migrations/0026_tokens_into_keys.sql"

var (
	tikAuthorPerms = []string{"posts.read", "posts.write", "pages.read", "media.read", "media.write"}
	tikEditorPerms = []string{"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write"}
	tikAdminPerms  = []string{
		"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write",
		"products.read", "products.write", "orders.read", "themes.read", "themes.write",
		"plugins.read", "plugins.write", "settings.read", "settings.write", "logs.read",
	}
)

// tikSeed is one personal API token as it exists before 0026 runs.
type tikSeed struct {
	label      string
	owner      string // key into the owners map: "admin" or "author"
	name       string
	note       string
	scopes     []string
	raw        string
	createdAt  time.Time
	lastUsedAt *time.Time
	lastUsedIP string
	expiresAt  *time.Time
	revokedAt  *time.Time
	wantPerms  []string
	wantRole   string // expected agents.role_cap, "" means NULL
}

// tikRow is the joined agent and agent key that 0026 must create for one token.
type tikRow struct {
	agentID     int64
	agentName   string
	description string
	ownerID     sql.NullInt64
	createdBy   sql.NullInt64
	status      string
	perms       string
	keyID       int64
	prefix      string
	keyCreated  time.Time
	keyCreator  sql.NullInt64
	lastUsedAt  sql.NullTime
	lastUsedIP  string
	expiresAt   sql.NullTime
	revokedAt   sql.NullTime
	revokedBy   sql.NullInt64
}

func tikSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func tikArrayLiteral(values []string) string {
	return "{" + strings.Join(values, ",") + "}"
}

func tikConcat(lists ...[]string) []string {
	var out []string
	for _, list := range lists {
		out = append(out, list...)
	}
	return out
}

// tikSortedSet returns the sorted, de-duplicated members of list.
func tikSortedSet(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range list {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// tikSameTime compares a nullable column with an expected optional timestamp.
func tikSameTime(got sql.NullTime, want *time.Time) bool {
	if want == nil {
		return !got.Valid
	}
	return got.Valid && got.Time.Equal(*want)
}

// applyTokensIntoKeysMigration runs the 0026 SQL that ships in the embedded
// migrations. It fails with a clear message while the file does not exist.
func applyTokensIntoKeysMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlText, err := embeddedMigrationFiles.ReadFile(tokensIntoKeysMigrationPath)
	if err != nil {
		t.Fatalf("migration 0026_tokens_into_keys.sql is missing from internal/data/migrations (ticket 22 requires it): %v", err)
	}
	if err := db.Exec(string(sqlText)).Error; err != nil {
		t.Fatalf("apply 0026_tokens_into_keys.sql: %v", err)
	}
}

// newTokensIntoKeysFixture migrates a fresh database and seeds the api_tokens
// rows. The seeds cover a duplicate name, a revoked and an expired token, two
// owners, every scope combination in the mapping table, an unknown scope, and
// names that are 80 characters long.
func newTokensIntoKeysFixture(t *testing.T) (*gorm.DB, map[string]core.User, []tikSeed) {
	t.Helper()
	db := newAccountsMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB on a fresh database: %v", err)
	}

	owners := map[string]core.User{}
	for key, spec := range map[string]struct{ username, role string }{
		"admin":  {"tik-owner-admin", "admin"},
		"author": {"tik-owner-author", "author"},
	} {
		u := core.User{Username: spec.username, Email: spec.username + "@example.test", Role: spec.role, Provider: "local"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create owner %s: %v", spec.username, err)
		}
		owners[key] = u
	}

	t0 := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := t0.Add(d); return &v }
	long := strings.Repeat("長", 80)
	seeds := []tikSeed{
		{label: "admin duplicate name, content_read", owner: "admin", name: "CI bot", note: "nightly sync", scopes: []string{"content_read"},
			raw: "jly_tik-admin-content-0001", createdAt: t0, lastUsedAt: at(2 * time.Hour), lastUsedIP: "203.0.113.0",
			wantPerms: tikConcat([]string{"posts.read", "pages.read"}), wantRole: ""},
		{label: "admin same name, orders_read plus author", owner: "admin", name: "CI bot", scopes: []string{"orders_read", "author"},
			raw: "jly_tik-admin-orders-0002", createdAt: t0.Add(time.Hour),
			wantPerms: tikConcat([]string{"orders.read"}, tikAuthorPerms), wantRole: "author"},
		{label: "admin revoked editor token", owner: "admin", name: "旧權杖", scopes: []string{"editor"},
			raw: "jly_tik-admin-revoked-0003", createdAt: t0, revokedAt: at(3 * time.Hour),
			wantPerms: tikEditorPerms, wantRole: "editor"},
		{label: "admin expired admin token", owner: "admin", name: "過期權杖", scopes: []string{"admin"},
			raw: "jly_tik-admin-expired-0004", createdAt: t0, expiresAt: at(-24 * time.Hour),
			wantPerms: tikAdminPerms, wantRole: "admin"},
		{label: "author duplicate name, author plus unknown scope", owner: "author", name: "CI bot", scopes: []string{"author", "bogus"},
			raw: "jly_tik-author-bot-0005", createdAt: t0, wantPerms: tikAuthorPerms, wantRole: "author"},
		{label: "author name differs only by case, unknown scope only", owner: "author", name: "ci bot", scopes: []string{"weird"},
			raw: "jly_tik-author-case-0006", createdAt: t0, wantPerms: nil, wantRole: ""},
		{label: "author 80-character name", owner: "author", name: long, scopes: []string{"content_read"},
			raw: "jly_tik-author-long-0007", createdAt: t0, wantPerms: tikConcat([]string{"posts.read", "pages.read"}), wantRole: ""},
		{label: "admin 80-character duplicate name, empty scopes", owner: "admin", name: long, scopes: []string{},
			raw: "jly_tik-admin-long-0008", createdAt: t0, wantPerms: nil, wantRole: ""},
	}

	for _, s := range seeds {
		if len(s.raw) < 4 {
			t.Fatalf("seed %s has a raw token shorter than 4 characters", s.label)
		}
		if err := db.Exec(`INSERT INTO api_tokens (user_id, name, note, token_hash, last4, scopes, expires_at, revoked_at,
			last_used_at, last_used_ip_masked, created_at)
			VALUES (?, ?, ?, ?, ?, ?::text[], ?, ?, ?, ?, ?)`,
			owners[s.owner].ID, s.name, s.note, tikSHA256Hex(s.raw), s.raw[len(s.raw)-4:], tikArrayLiteral(s.scopes),
			s.expiresAt, s.revokedAt, s.lastUsedAt, s.lastUsedIP, s.createdAt).Error; err != nil {
			t.Fatalf("seed api_tokens row %s: %v", s.label, err)
		}
	}
	return db, owners, seeds
}

func tikCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func tikSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var s string
	if err := db.Raw(`SELECT
		(SELECT coalesce(string_agg(id::text || ':' || name || ':' || status || ':' || array_to_string(permissions, ','), '|' ORDER BY id), '') FROM agents)
		|| ' # ' ||
		(SELECT coalesce(string_agg(id::text || ':' || agent_id::text || ':' || key_hash || ':' || prefix, '|' ORDER BY id), '') FROM agent_keys)`).Row().Scan(&s); err != nil {
		t.Fatalf("snapshot agents and agent_keys: %v", err)
	}
	return s
}

func TestTokensIntoKeysMigrationMapsEveryToken(t *testing.T) {
	db, owners, seeds := newTokensIntoKeysFixture(t)
	applyTokensIntoKeysMigration(t, db)

	if n := tikCount(t, db, "SELECT count(*) FROM api_tokens"); n != int64(len(seeds)) {
		t.Errorf("api_tokens has %d rows after 0026, want %d: the migration must keep the old table", n, len(seeds))
	}
	if n := tikCount(t, db, "SELECT count(*) FROM agents"); n != int64(len(seeds)) {
		t.Errorf("agents has %d rows, want one per token (%d)", n, len(seeds))
	}
	if n := tikCount(t, db, "SELECT count(*) FROM agent_keys"); n != int64(len(seeds)) {
		t.Errorf("agent_keys has %d rows, want one per token (%d)", n, len(seeds))
	}
	if n := tikCount(t, db, `SELECT count(*) FROM (SELECT lower(name) FROM agents WHERE deleted_at IS NULL GROUP BY lower(name) HAVING count(*) > 1) d`); n != 0 {
		t.Errorf("%d agent names collide case-insensitively, want every active name unique", n)
	}
	if n := tikCount(t, db, "SELECT count(*) FROM agents WHERE char_length(name) > 80 OR char_length(name) = 0"); n != 0 {
		t.Errorf("%d agent names are empty or longer than 80 characters", n)
	}

	for _, s := range seeds {
		var r tikRow
		err := db.Raw(`SELECT a.id, a.name, a.description, a.owner_id, a.created_by, a.status, array_to_string(a.permissions, ','),
			k.id, k.prefix, k.created_at, k.created_by, k.last_used_at, k.last_used_ip_masked, k.expires_at, k.revoked_at, k.revoked_by
			FROM agent_keys k JOIN agents a ON a.id = k.agent_id WHERE k.key_hash = ?`, tikSHA256Hex(s.raw)).Row().Scan(
			&r.agentID, &r.agentName, &r.description, &r.ownerID, &r.createdBy, &r.status, &r.perms,
			&r.keyID, &r.prefix, &r.keyCreated, &r.keyCreator, &r.lastUsedAt, &r.lastUsedIP, &r.expiresAt, &r.revokedAt, &r.revokedBy)
		if err != nil {
			t.Errorf("%s: no agent_keys row with the token's key_hash: %v", s.label, err)
			continue
		}
		owner := owners[s.owner]
		if r.description != s.note {
			t.Errorf("%s: agent description=%q, want the token note %q", s.label, r.description, s.note)
		}
		if !r.ownerID.Valid || r.ownerID.Int64 != int64(owner.ID) || !r.createdBy.Valid || r.createdBy.Int64 != int64(owner.ID) {
			t.Errorf("%s: owner_id=%v created_by=%v, want both %d", s.label, r.ownerID, r.createdBy, owner.ID)
		}
		if r.status != "active" {
			t.Errorf("%s: agent status=%q, want active even when the token is revoked or expired", s.label, r.status)
		}
		if got := tikSortedSet(strings.Split(r.perms, ",")); strings.Join(got, ",") != strings.Join(tikSortedSet(s.wantPerms), ",") {
			t.Errorf("%s: agent permissions=%v, want %v", s.label, got, tikSortedSet(s.wantPerms))
		}
		if wantPrefix := "jly_" + s.raw[len(s.raw)-4:]; r.prefix != wantPrefix {
			t.Errorf("%s: agent_keys.prefix=%q, want %q", s.label, r.prefix, wantPrefix)
		}
		if !r.keyCreated.Equal(s.createdAt) {
			t.Errorf("%s: agent_keys.created_at=%v, want the token's %v", s.label, r.keyCreated, s.createdAt)
		}
		if !r.keyCreator.Valid || r.keyCreator.Int64 != int64(owner.ID) {
			t.Errorf("%s: agent_keys.created_by=%v, want the token owner %d", s.label, r.keyCreator, owner.ID)
		}
		if r.lastUsedIP != s.lastUsedIP || !tikSameTime(r.lastUsedAt, s.lastUsedAt) {
			t.Errorf("%s: last use ip=%q at=%v, want ip=%q at=%v", s.label, r.lastUsedIP, r.lastUsedAt, s.lastUsedIP, s.lastUsedAt)
		}
		if !tikSameTime(r.expiresAt, s.expiresAt) {
			t.Errorf("%s: agent_keys.expires_at=%v, want the token's expiry", s.label, r.expiresAt)
		}
		if !tikSameTime(r.revokedAt, s.revokedAt) {
			t.Errorf("%s: agent_keys.revoked_at=%v, want the token's revocation", s.label, r.revokedAt)
		}
		if r.revokedBy.Valid {
			t.Errorf("%s: agent_keys.revoked_by=%d, want empty because the migration does not invent a revoker", s.label, r.revokedBy.Int64)
		}
	}
}

func TestTokensIntoKeysMigrationRerunIsIdempotent(t *testing.T) {
	db, _, seeds := newTokensIntoKeysFixture(t)
	applyTokensIntoKeysMigration(t, db)
	before := tikSnapshot(t, db)

	applyTokensIntoKeysMigration(t, db)
	if after := tikSnapshot(t, db); after != before {
		t.Errorf("rerunning 0026 changed agents or agent_keys:\nbefore=%s\nafter=%s", before, after)
	}
	if n := tikCount(t, db, "SELECT count(*) FROM agent_keys"); n != int64(len(seeds)) {
		t.Errorf("rerunning 0026 left %d agent_keys rows, want %d", n, len(seeds))
	}
	if n := tikCount(t, db, "SELECT count(*) FROM api_tokens"); n != int64(len(seeds)) {
		t.Errorf("rerunning 0026 changed api_tokens to %d rows, want %d kept", n, len(seeds))
	}
}

func TestTokensIntoKeysMigrationRelaxesKeyPrefixCheck(t *testing.T) {
	db, _, _ := newTokensIntoKeysFixture(t)
	applyTokensIntoKeysMigration(t, db)

	var agentID int64
	if err := db.Raw("SELECT min(id) FROM agents").Row().Scan(&agentID); err != nil {
		t.Fatalf("read a migrated agent: %v", err)
	}
	insert := func(prefix string, n int) error {
		return db.Exec("INSERT INTO agent_keys (agent_id, key_hash, prefix) VALUES (?, ?, ?)", agentID, tikSHA256Hex(fmt.Sprintf("tik-probe-%d", n)), prefix).Error
	}
	for n, prefix := range []string{"jly_abcd", "jly_abcdefgh", "jla_abcdefgh"} {
		if err := insert(prefix, n); err != nil {
			t.Errorf("agent_keys rejected prefix %q after 0026: %v", prefix, err)
		}
	}
	for n, prefix := range []string{"jly_abcdefghi", "jly_abc", "xyz_abcdefgh"} {
		if err := insert(prefix, 100+n); err == nil {
			t.Errorf("agent_keys accepted prefix %q; want the check to reject it", prefix)
		}
	}
}

func TestTokensIntoKeysMigrationSetsRoleCapFromHighestCappingScope(t *testing.T) {
	db, _, seeds := newTokensIntoKeysFixture(t)
	applyTokensIntoKeysMigration(t, db)

	for _, s := range seeds {
		var got sql.NullString
		err := db.Raw(`SELECT a.role_cap FROM agent_keys k JOIN agents a ON a.id = k.agent_id WHERE k.key_hash = ?`,
			tikSHA256Hex(s.raw)).Row().Scan(&got)
		if err != nil {
			t.Errorf("%s: no agent for the token after 0026: %v", s.label, err)
			continue
		}
		if s.wantRole == "" {
			if got.Valid {
				t.Errorf("%s: agents.role_cap=%q, want NULL because the scopes carry no admin, editor or author", s.label, got.String)
			}
			continue
		}
		if !got.Valid || got.String != s.wantRole {
			t.Errorf("%s: agents.role_cap=%v, want %q (the highest of admin, editor and author in scopes %v)", s.label, got, s.wantRole, s.scopes)
		}
	}
}

func TestTokensIntoKeysMigrationRoleCapAcceptsOnlyStaffRoles(t *testing.T) {
	db, _, _ := newTokensIntoKeysFixture(t)
	applyTokensIntoKeysMigration(t, db)

	var agentID int64
	if err := db.Raw("SELECT min(id) FROM agents").Row().Scan(&agentID); err != nil {
		t.Fatalf("read a migrated agent: %v", err)
	}
	set := func(value any) error {
		return db.Exec("UPDATE agents SET role_cap = ? WHERE id = ?", value, agentID).Error
	}
	for _, value := range []any{"admin", "editor", "author", nil} {
		if err := set(value); err != nil {
			t.Errorf("agents.role_cap rejected %v after 0026: %v", value, err)
		}
	}
	for _, value := range []string{"owner", "superadmin", "ADMIN", "content_read", "orders_read", ""} {
		if err := set(value); err == nil {
			t.Errorf("agents.role_cap accepted %q; want the check to allow only admin, editor, author or NULL", value)
		}
	}
}
