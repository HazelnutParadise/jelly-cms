package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	testPGHost   = "127.0.0.1"
	testPGPort   = "55432"
	testPGUser   = "jelly_test"
	testPGPass   = "jelly-local-acceptance"
	testPGAdmin  = "jelly_acceptance"
	testDBPrefix = "jelly_oauth_recovery_cli_"
)

var testDBSeq atomic.Uint64

func uniqueDBName(t *testing.T) string {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s%d_%d_%s", testDBPrefix, os.Getpid(), testDBSeq.Add(1), hex.EncodeToString(random[:]))
}

type cliResult struct {
	code           int
	stdout, stderr string
}

var cliPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "recover-oauth-cli-")
	if err != nil {
		panic(err)
	}
	cliPath = filepath.Join(dir, "recover-oauth")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", cliPath, ".")
	output, buildErr := cmd.CombinedOutput()
	cancel()
	if buildErr != nil {
		_ = os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "build recover-oauth: %v\n%s", buildErr, output)
		os.Exit(2)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func runCLI(t *testing.T, _ string, env map[string]string, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Dir = t.TempDir() // The explicit --site-dir must be the only site lookup path.
	cmd.Env = cleanEnv(os.Environ())
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdoutBuf, &stderrBuf
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out for %v: %v", args, ctx.Err())
	}
	if err == nil {
		return cliResult{0, stdoutBuf.String(), stderrBuf.String()}
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return cliResult{ee.ExitCode(), stdoutBuf.String(), stderrBuf.String()}
	}
	if err != nil {
		t.Fatalf("run CLI %v: %v", args, err)
	}
	return cliResult{}
}

func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(v, "JELLY_TEST_POSTGRES=") && !strings.HasPrefix(v, "JWT_SECRET=") {
			out = append(out, v)
		}
	}
	return out
}

func TestHelpAndArgumentExitCodes(t *testing.T) {
	site := t.TempDir()
	before := treeNames(t, site)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"no args", nil, []string{"list", "issue", "--site-dir", "--user-id", "--provider", "JWT_SECRET", "auth-secrets.json", "範例"}},
		{"help", []string{"--help"}, []string{"list", "issue", "--site-dir", "--user-id", "--provider", "JWT_SECRET", "auth-secrets.json", "範例"}},
		{"list help", []string{"list", "--help"}, []string{"--limit 50", "--after-id 0", `"users":[]`, `"next_after_id":null`, "範例"}},
		{"issue help", []string{"issue", "--help"}, []string{"--user-id", "--provider", "expires_at", "grant", "JWT_SECRET", "auth-secrets.json", "範例"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runCLI(t, site, nil, tc.args...)
			if r.code != 0 {
				t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
			}
			text := r.stdout
			for _, s := range tc.want {
				if !strings.Contains(text, s) {
					t.Errorf("help missing %q", s)
				}
			}
		})
	}
	if got := treeNames(t, site); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("help created files: before=%v after=%v", before, got)
	}

	cases := map[string][]string{
		"unknown command": {"wat"}, "unknown top flag": {"--what"},
		"list missing site": {"list"}, "list blank site": {"list", "--site-dir", "  "}, "unknown list flag": {"list", "--site-dir", "x", "--wat"}, "list extra arg": {"list", "--site-dir", "x", "extra"}, "limit zero": {"list", "--site-dir", "x", "--limit", "0"}, "limit over max": {"list", "--site-dir", "x", "--limit", "501"}, "after negative": {"list", "--site-dir", "x", "--after-id", "-1"}, "after overflow": {"list", "--site-dir", "x", "--after-id", "18446744073709551616"},
		"issue missing site": {"issue", "--user-id", "1", "--provider", "google"}, "issue missing user": {"issue", "--site-dir", "x", "--provider", "google"}, "issue missing provider": {"issue", "--site-dir", "x", "--user-id", "1"}, "issue zero id": {"issue", "--site-dir", "x", "--user-id", "0", "--provider", "google"}, "issue negative id": {"issue", "--site-dir", "x", "--user-id", "-1", "--provider", "google"}, "issue overflow id": {"issue", "--site-dir", "x", "--user-id", "18446744073709551616", "--provider", "google"}, "unknown provider": {"issue", "--site-dir", "x", "--user-id", "1", "--provider", "local"}, "unknown issue flag": {"issue", "--site-dir", "x", "--user-id", "1", "--provider", "google", "--wat"}, "issue extra arg": {"issue", "--site-dir", "x", "--user-id", "1", "--provider", "google", "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			r := runCLI(t, site, nil, args...)
			if r.code != 2 {
				t.Fatalf("returned exit code %d, want 2", r.code)
			}
			if !strings.Contains(r.stderr, "用法") {
				t.Errorf("parameter failure did not include usage")
			}
			if strings.Contains(r.stdout, "grant") {
				t.Fatalf("stdout leaked grant: %q", r.stdout)
			}
		})
	}
}

func TestCLIListAndIssueAgainstDedicatedPostgres(t *testing.T) {
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to use the dedicated PostgreSQL instance")
	}
	admin, err := data.Open(config.DatabaseConfig{Host: testPGHost, Port: testPGPort, User: testPGUser, Password: testPGPass, Name: testPGAdmin, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin DB: %v", err)
	}
	admin.Logger = admin.Logger.LogMode(logger.Silent)
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	dbName := uniqueDBName(t)
	if err = admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		_ = adminSQL.Close()
		t.Fatalf("create isolated DB: %v", err)
	}
	var version int
	t.Cleanup(func() {
		_ = adminSQL.Close()
		closer, e := data.Open(config.DatabaseConfig{Host: testPGHost, Port: testPGPort, User: testPGUser, Password: testPGPass, Name: testPGAdmin, Timezone: "UTC"})
		if e != nil {
			t.Errorf("reopen admin DB for cleanup: %v", e)
			return
		}
		defer closeDB(t, closer)
		_ = closer.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, dbName)).Error
		_ = closer.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", dbName).Error
		drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)
		if version >= 130000 {
			drop += ` WITH (FORCE)`
		}
		if e = closer.Exec(drop).Error; e != nil {
			t.Errorf("drop own DB: %v", e)
		}
	})
	if err = admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	db, err := data.Open(config.DatabaseConfig{Host: testPGHost, Port: testPGPort, User: testPGUser, Password: testPGPass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated DB: %v", err)
	}
	if err = data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated DB: %v", err)
	}
	db.Logger = db.Logger.LogMode(logger.Silent)
	defer closeDB(t, db)
	site := makeSite(t, dbName)
	var users []core.User
	for i := 1; i <= 55; i++ {
		provider := "google"
		if i == 2 {
			provider = "github"
		}
		u := core.User{Username: fmt.Sprintf("legacy-%02d", i), Email: fmt.Sprintf("legacy-%02d@example.test", i), Password: "legacy-password-hash", Role: "author", Provider: provider}
		if e := db.Create(&u).Error; e != nil {
			t.Fatal(e)
		}
		users = append(users, u)
	}
	local := core.User{Username: "local-user", Email: "local@example.test", Password: "hashed-local-password", Role: "admin", Provider: "local"}
	if err = db.Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	unknown := core.User{Username: "unknown-provider", Email: "unknown-provider@example.test", Password: "legacy-password-hash", Role: "author", Provider: "mastodon"}
	if err = db.Create(&unknown).Error; err != nil {
		t.Fatal(err)
	}
	post := core.Post{Title: "Preserved legacy post", Slug: "preserved-legacy-post", Content: "unchanged article body", Type: "post", Status: "published", AuthorID: users[0].ID}
	if err = db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	// Any active identity excludes a legacy account; a deleted identity remains history and does not.
	active := users[2]
	if err = db.Exec(`INSERT INTO oauth_identities (user_id,provider,subject) VALUES (?, 'github', 'active-subject')`, active.ID).Error; err != nil {
		t.Fatal(err)
	}
	deleted := users[3]
	if err = db.Exec(`INSERT INTO oauth_identities (user_id,provider,subject,deleted_at) VALUES (?, 'google', 'deleted-subject', now())`, deleted.ID).Error; err != nil {
		t.Fatal(err)
	}
	full := runCLI(t, site, nil, "list", "--site-dir", site)
	listReady := full.code == 0
	if !listReady {
		if strings.Contains(full.stderr, "Recovery operation is not implemented") {
			t.Errorf("list feature RED: %s", full.stderr)
		} else {
			t.Fatalf("list exit=%d stdout=%q stderr=%q", full.code, full.stdout, full.stderr)
		}
	}
	if listReady {
		if full.stderr != "" {
			t.Errorf("successful list emitted stderr diagnostics")
		}
		var page struct {
			Users []map[string]any `json:"users"`
			Next  *uint            `json:"next_after_id"`
		}
		if err = json.Unmarshal([]byte(full.stdout), &page); err != nil {
			t.Fatalf("list JSON: %v; stderr=%s", err, full.stderr)
		}
		if len(page.Users) != 50 || page.Next == nil || *page.Next != users[50].ID {
			t.Fatalf("default page size/cursor: len=%d next=%v", len(page.Users), page.Next)
		}
		seen := map[uint]bool{}
		for _, row := range page.Users {
			id := uint(row["id"].(float64))
			seen[id] = true
			for _, k := range []string{"id", "username", "email", "provider"} {
				if _, ok := row[k]; !ok {
					t.Errorf("row missing %s: %v", k, row)
				}
			}
			for k := range row {
				if k != "id" && k != "username" && k != "email" && k != "provider" {
					t.Errorf("unexpected field %s in public list row", k)
				}
			}
		}
		if seen[active.ID] || !seen[deleted.ID] {
			t.Fatalf("active/deleted identity filter wrong")
		}
		var collected []uint
		cursor := uint(0)
		for pageNo := 0; pageNo < 40; pageNo++ {
			args := []string{"list", "--site-dir", site, "--limit", "2", "--after-id", strconv.FormatUint(uint64(cursor), 10)}
			res := runCLI(t, site, nil, args...)
			assertImplemented(t, res, "list pagination")
			var p struct {
				Users []map[string]any `json:"users"`
				Next  *uint            `json:"next_after_id"`
			}
			if e := json.Unmarshal([]byte(res.stdout), &p); e != nil {
				t.Fatalf("page %d JSON: %v", pageNo, e)
			}
			for _, row := range p.Users {
				collected = append(collected, uint(row["id"].(float64)))
			}
			if p.Next == nil {
				break
			}
			if len(p.Users) == 0 || *p.Next != collected[len(collected)-1] {
				t.Fatalf("page %d cursor does not match last row: rows=%v cursor=%v", pageNo, p.Users, p.Next)
			}
			cursor = *p.Next
			if pageNo == 39 {
				t.Fatal("pagination did not terminate")
			}
		}
		wantIDs := make([]uint, 0, len(users)-1)
		for _, u := range users {
			if u.ID != active.ID {
				wantIDs = append(wantIDs, u.ID)
			}
		}
		if len(collected) != len(wantIDs) {
			t.Fatalf("pagination returned %d IDs, want %d", len(collected), len(wantIDs))
		}
		for i := range wantIDs {
			if collected[i] != wantIDs[i] {
				t.Fatalf("pagination mismatch at %d: got %v want %v", i, collected, wantIDs)
			}
		}
		second := runCLI(t, site, nil, "list", "--site-dir", site, "--limit", "2", "--after-id", strconv.FormatUint(uint64(users[0].ID), 10))
		assertImplemented(t, second, "list")
		var secondPage struct {
			Users []map[string]any `json:"users"`
			Next  *uint            `json:"next_after_id"`
		}
		if err = json.Unmarshal([]byte(second.stdout), &secondPage); err != nil {
			t.Fatalf("two-row page JSON: %v", err)
		}
		if len(secondPage.Users) != 2 || uint(secondPage.Users[0]["id"].(float64)) != users[1].ID || uint(secondPage.Users[1]["id"].(float64)) != users[3].ID {
			t.Fatalf("after-id page is not sorted/filtered")
		}
		all := runCLI(t, site, nil, "list", "--site-dir", site, "--limit", "500")
		assertImplemented(t, all, "list")
		var allPage struct {
			Users []map[string]any `json:"users"`
			Next  *uint            `json:"next_after_id"`
		}
		if err = json.Unmarshal([]byte(all.stdout), &allPage); err != nil {
			t.Fatalf("500-row page JSON: %v", err)
		}
		if len(allPage.Users) != 54 || allPage.Next != nil {
			t.Fatalf("limit 500 should return 54 eligible users with null cursor, got %d next=%v", len(allPage.Users), allPage.Next)
		}
		empty := runCLI(t, site, nil, "list", "--site-dir", site, "--after-id", strconv.FormatUint(uint64(users[54].ID+100), 10))
		assertImplemented(t, empty, "list")
		var emptyPage struct {
			Users []map[string]any `json:"users"`
			Next  *uint            `json:"next_after_id"`
		}
		if err = json.Unmarshal([]byte(empty.stdout), &emptyPage); err != nil || emptyPage.Users == nil || len(emptyPage.Users) != 0 || emptyPage.Next != nil {
			t.Fatalf("empty list must be [] with null cursor (decode error type %T)", err)
		}
	}
	for i, provider := range []string{"google", "github"} {
		t.Run("issue_"+provider, func(t *testing.T) {
			target := users[i]
			env := map[string]string{}
			effectiveSecret := strings.Repeat("J", 40)
			if provider == "github" {
				effectiveSecret = "oauth-cli-jwt-secret-override-0123456789"
				env["JWT_SECRET"] = effectiveSecret
			}
			beforeDB := snapshotSiteDB(t, db)
			beforeFiles := snapshotSiteFiles(t, site)
			beforeSecrets, e := os.ReadFile(filepath.Join(site, "data", "auth-secrets.json"))
			if e != nil {
				t.Fatal(e)
			}
			r := runCLI(t, site, env, "issue", "--site-dir", site, "--user-id", strconv.FormatUint(uint64(target.ID), 10), "--provider", provider)
			if r.code != 0 && strings.Contains(r.stderr, "Recovery operation is not implemented") {
				t.Fatalf("issue feature RED: %s", r.stderr)
			}
			assertImplemented(t, r, "issue")
			if r.stderr != "" {
				t.Errorf("successful issue emitted stderr diagnostics")
			}
			var got map[string]any
			if e := json.Unmarshal([]byte(r.stdout), &got); e != nil {
				t.Fatalf("issue JSON: %v", e)
			}
			if len(got) != 6 {
				t.Fatalf("issue output has %d fields, want 6", len(got))
			}
			for _, key := range []string{"user_id", "username", "email", "provider", "expires_at", "grant"} {
				if _, ok := got[key]; !ok {
					t.Errorf("issue output missing %q", key)
				}
			}
			grant, _ := got["grant"].(string)
			if grant == "" || strings.Count(r.stdout, grant) != 1 {
				t.Fatalf("grant empty/repeated")
			}
			if strings.Contains(r.stderr, grant) {
				t.Errorf("grant leaked to stderr")
			}
			expires, e := time.Parse(time.RFC3339, fmt.Sprint(got["expires_at"]))
			if e != nil {
				t.Errorf("expires_at is not RFC3339: %v", e)
			} else if time.Until(expires) <= 0 || time.Until(expires) > 10*time.Minute {
				t.Errorf("expires_at is outside the next ten minutes: %v", expires)
			}
			if got["user_id"] != float64(target.ID) || got["username"] != target.Username || got["email"] != target.Email || got["provider"] != provider {
				t.Errorf("issue returned a different user or provider")
			}
			verifyGrant(t, grant, target.ID, target.Username, target.Email, provider, expires, []byte(effectiveSecret))
			for _, secret := range []string{testPGPass, "legacy-password-hash", "active-subject", strings.Repeat("J", 40), effectiveSecret} {
				if strings.Contains(r.stdout+r.stderr, secret) {
					t.Errorf("secret leaked to output")
				}
			}
			if after := snapshotSiteDB(t, db); beforeDB != after {
				t.Errorf("issue modified site database rows")
			}
			if after := snapshotSiteFiles(t, site); !equalFileSnapshots(beforeFiles, after) {
				t.Errorf("issue modified or created site files")
			}
			if after, e := os.ReadFile(filepath.Join(site, "data", "auth-secrets.json")); e != nil || !bytes.Equal(beforeSecrets, after) {
				t.Errorf("issue changed key file: %v", e)
			}
			if provider == "google" {
				emptyOverride := runCLI(t, site, map[string]string{"JWT_SECRET": ""}, "issue", "--site-dir", site, "--user-id", strconv.FormatUint(uint64(target.ID), 10), "--provider", provider)
				expectCLIJSON(t, emptyOverride, "empty JWT_SECRET fallback")
				var fallback map[string]any
				if e := json.Unmarshal([]byte(emptyOverride.stdout), &fallback); e != nil {
					t.Fatal(e)
				}
				verifyGrant(t, fmt.Sprint(fallback["grant"]), target.ID, target.Username, target.Email, provider, mustTime(t, fallback["expires_at"]), []byte(strings.Repeat("J", 40)))
			}
			for _, reject := range []struct {
				name     string
				id       uint
				provider string
			}{
				{"nonexistent user", 999999999, provider}, {"local user", local.ID, "google"}, {"already linked", active.ID, active.Provider}, {"wrong provider", target.ID, otherProvider(provider)}, {"unknown legacy provider", unknown.ID, "google"},
			} {
				rejectDB := snapshotSiteDB(t, db)
				rejectFiles := snapshotSiteFiles(t, site)
				bad := runCLI(t, site, nil, "issue", "--site-dir", site, "--user-id", strconv.FormatUint(uint64(reject.id), 10), "--provider", reject.provider)
				expectSafeFailure(t, bad, reject.name, []string{testPGPass, "legacy-password-hash", "active-subject"})
				if after := snapshotSiteDB(t, db); rejectDB != after {
					t.Errorf("%s changed database state", reject.name)
				}
				if after := snapshotSiteFiles(t, site); !equalFileSnapshots(rejectFiles, after) {
					t.Errorf("%s changed site files", reject.name)
				}
			}
		})
	}
	// Secrets must remain read-only, and an explicit non-empty environment key can work without a file.
	t.Run("override_without_key_file", func(t *testing.T) {
		overrideSite := cloneSiteWithoutKey(t, site)
		overrideSecret := "override-with-no-secret-file-0123456789"
		noFile := runCLI(t, overrideSite, map[string]string{"JWT_SECRET": overrideSecret}, "issue", "--site-dir", overrideSite, "--user-id", strconv.FormatUint(uint64(users[0].ID), 10), "--provider", "google")
		expectCLIJSON(t, noFile, "override without key file")
		var noFileResult map[string]any
		if e := json.Unmarshal([]byte(noFile.stdout), &noFileResult); e != nil {
			t.Fatal(e)
		}
		verifyGrant(t, fmt.Sprint(noFileResult["grant"]), users[0].ID, users[0].Username, users[0].Email, "google", mustTime(t, noFileResult["expires_at"]), []byte(overrideSecret))
		if _, e := os.Stat(filepath.Join(overrideSite, "data", "auth-secrets.json")); !os.IsNotExist(e) {
			t.Errorf("override created key file: %v", e)
		}
	})

	// Malformed, missing, or over-permissive key files must fail without leaking secrets.
	for _, tc := range []struct {
		name     string
		contents []byte
		mode     os.FileMode
	}{
		{"missing key", nil, 0600}, {"damaged key", []byte(`{"version":1,"session_secret":"bad","jwt_secret":"bad"}`), 0600},
		{"wide key permissions", []byte(fmt.Sprintf(`{"version":1,"session_secret":%q,"jwt_secret":%q}`, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("S", 40))), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("J", 40))))), 0644},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			keySite := cloneSite(t, site)
			keyPath := filepath.Join(keySite, "data", "auth-secrets.json")
			_ = os.Remove(keyPath)
			if tc.contents != nil {
				if e := os.WriteFile(keyPath, tc.contents, tc.mode); e != nil {
					t.Fatal(e)
				}
			}
			if runtime.GOOS == "windows" && tc.name == "wide key permissions" {
				t.Skip("Unix key-file permissions are not available on Windows")
			}
			beforeFiles := snapshotSiteFiles(t, keySite)
			bad := runCLI(t, keySite, nil, "issue", "--site-dir", keySite, "--user-id", strconv.FormatUint(uint64(users[0].ID), 10), "--provider", "google")
			expectSafeFailure(t, bad, tc.name, []string{testPGPass, "legacy-password-hash", "active-subject", "jwt_secret"})
			if after := snapshotSiteFiles(t, keySite); !equalFileSnapshots(beforeFiles, after) {
				t.Errorf("%s changed site files", tc.name)
			}
			if _, e := os.Stat(keyPath); tc.name == "missing key" && !os.IsNotExist(e) {
				t.Errorf("tool created missing key: %v", e)
			}
			if tc.contents != nil {
				if after, e := os.ReadFile(keyPath); e != nil || !bytes.Equal(after, tc.contents) {
					t.Errorf("tool changed invalid key file: %v", e)
				}
			}
		})
	}
	// Missing config and schema/DB failures must reject without migration or creating key files.
	t.Run("missing_config", func(t *testing.T) {
		bad := t.TempDir()
		if e := os.Mkdir(filepath.Join(bad, "data"), 0700); e != nil {
			t.Fatal(e)
		}
		before := snapshotSiteFiles(t, bad)
		expectSafeFailure(t, runCLI(t, bad, nil, "list", "--site-dir", bad), "missing config", []string{testPGPass})
		if after := snapshotSiteFiles(t, bad); !equalFileSnapshots(before, after) {
			t.Errorf("missing-config command changed site files")
		}
	})
	t.Run("damaged_config", func(t *testing.T) {
		bad := makeSite(t, dbName)
		if e := os.WriteFile(filepath.Join(bad, "data", "config.json"), []byte("{"), 0600); e != nil {
			t.Fatal(e)
		}
		before := snapshotSiteFiles(t, bad)
		expectSafeFailure(t, runCLI(t, bad, nil, "list", "--site-dir", bad), "damaged config", []string{testPGPass})
		if after := snapshotSiteFiles(t, bad); !equalFileSnapshots(before, after) {
			t.Errorf("damaged-config command changed site files")
		}
	})
	t.Run("unavailable_database", func(t *testing.T) {
		bad := makeSite(t, dbName)
		var cfg map[string]any
		b, _ := os.ReadFile(filepath.Join(bad, "data", "config.json"))
		_ = json.Unmarshal(b, &cfg)
		cfg["database"].(map[string]any)["port"] = "1"
		b, _ = json.Marshal(cfg)
		_ = os.WriteFile(filepath.Join(bad, "data", "config.json"), b, 0600)
		before := snapshotSiteFiles(t, bad)
		expectSafeFailure(t, runCLI(t, bad, nil, "list", "--site-dir", bad), "unavailable database", []string{testPGPass, testPGUser, testPGHost, dbName, "port=1", "connection refused", "SQL"})
		if after := snapshotSiteFiles(t, bad); !equalFileSnapshots(before, after) {
			t.Errorf("DB-connection failure changed site files")
		}
	})

	// A valid connection to an empty dedicated database must fail without creating schema.
	t.Run("missing_schema", func(t *testing.T) {
		bareName := uniqueDBName(t)
		if e := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, bareName)).Error; e != nil {
			t.Fatalf("create empty-schema database: %v", e)
		}
		t.Cleanup(func() {
			_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, bareName)).Error
			_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", bareName).Error
			drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, bareName)
			if version >= 130000 {
				drop += ` WITH (FORCE)`
			}
			if e := admin.Exec(drop).Error; e != nil {
				t.Errorf("drop empty-schema test DB: %v", e)
			}
		})
		bareSite := makeSite(t, bareName)
		beforeBareFiles := snapshotSiteFiles(t, bareSite)
		expectSafeFailure(t, runCLI(t, bareSite, nil, "list", "--site-dir", bareSite), "missing schema", []string{testPGPass})
		bareDB, e := data.Open(config.DatabaseConfig{Host: testPGHost, Port: testPGPort, User: testPGUser, Password: testPGPass, Name: bareName, Timezone: "UTC"})
		if e != nil {
			t.Fatalf("reopen empty-schema DB: %v", e)
		}
		bareDB.Logger = bareDB.Logger.LogMode(logger.Silent)
		var usersTable string
		if e = bareDB.Raw("SELECT COALESCE(to_regclass('public.users')::text,'')").Row().Scan(&usersTable); e != nil {
			t.Fatal(e)
		}
		closeDB(t, bareDB)
		if usersTable != "" {
			t.Errorf("empty-schema operation migrated users table unexpectedly")
		}
		if after := snapshotSiteFiles(t, bareSite); !equalFileSnapshots(beforeBareFiles, after) {
			t.Errorf("missing-schema operation changed site files")
		}
	})
}

func assertImplemented(t *testing.T, r cliResult, op string) {
	t.Helper()
	if r.code != 0 {
		if strings.Contains(r.stderr, "Recovery operation is not implemented") {
			t.Fatalf("%s feature RED: legal operation is not implemented", op)
		}
		t.Fatalf("%s returned unexpected exit code %d", op, r.code)
	}
}
func makeSite(t *testing.T, dbName string) string {
	t.Helper()
	d := t.TempDir()
	dataDir := filepath.Join(d, "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"database":{"host":%q,"port":%q,"user":%q,"password":%q,"name":%q,"timezone":"UTC"}}`, testPGHost, testPGPort, testPGUser, testPGPass, dbName)
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	raw := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("S", 40)))
	jwtRaw := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("J", 40)))
	doc := fmt.Sprintf(`{"version":1,"session_secret":%q,"jwt_secret":%q}`, raw, jwtRaw)
	if err := os.WriteFile(filepath.Join(dataDir, "auth-secrets.json"), []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	return d
}
func verifyGrant(t *testing.T, s string, id uint, username, email, provider string, expires time.Time, secret []byte) {
	t.Helper()
	tok, err := jwt.Parse(s, func(tok *jwt.Token) (any, error) {
		if tok.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("method %v", tok.Method)
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte("jelly-oauth-recovery-v1"))
		return mac.Sum(nil), nil
	})
	if err != nil {
		t.Fatalf("parse recovery grant: %v", err)
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok || !tok.Valid {
		t.Fatalf("invalid grant")
	}
	for k, w := range map[string]string{"purpose": "oauth-recovery", "iss": "jelly-cms", "aud": "jelly-oauth-recovery", "sub": strconv.FormatUint(uint64(id), 10), "provider": provider} {
		if fmt.Sprint(claims[k]) != w {
			t.Errorf("claim %s=%v want %s", k, claims[k], w)
		}
	}
	if claims["user_id"] != nil || claims["role"] != nil {
		t.Errorf("login claims found: %v", claims)
	}
	if claims["username"] != nil || claims["email"] != nil {
		t.Errorf("unexpected identity claims in grant: %v", claims)
	}
	jti, _ := claims["jti"].(string)
	decoded, e := base64.RawURLEncoding.DecodeString(jti)
	if e != nil || len(decoded) < 32 || len(jti) < 43 {
		t.Errorf("jti is not >=32-byte base64url randomness")
	}
	iat, e1 := numberDate(claims["iat"])
	exp, e2 := numberDate(claims["exp"])
	if e1 != nil || e2 != nil || exp.Sub(iat) > 600*time.Second || exp.Sub(iat) <= 0 || time.Now().Before(iat) || !time.Now().Before(exp) || !exp.Equal(expires) {
		t.Errorf("invalid grant lifetime iat=%v exp=%v", iat, exp)
	}
	// A different secret must not verify the token.
	wrong := hmac.New(sha256.New, []byte("definitely-wrong-jwt-key"))
	wrong.Write([]byte("jelly-oauth-recovery-v1"))
	if parsed, parseErr := jwt.Parse(s, func(*jwt.Token) (any, error) { return wrong.Sum(nil), nil }); parseErr == nil && parsed.Valid {
		t.Errorf("grant verified with the wrong key")
	}
	_ = username
	_ = email
}
func numberDate(v any) (time.Time, error) {
	f, ok := v.(float64)
	if !ok {
		return time.Time{}, fmt.Errorf("not numeric")
	}
	return time.Unix(int64(f), 0), nil
}
func snapshotSiteDB(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var snapshot []string
	for _, table := range []string{"users", "posts", "oauth_identities", "oauth_identity_audit_log"} {
		var rows string
		query := fmt.Sprintf("SELECT COALESCE(json_agg(to_jsonb(t) ORDER BY id), '[]'::json)::text FROM %s t", table)
		if e := db.Raw(query).Row().Scan(&rows); e != nil {
			t.Fatalf("snapshot %s including secret columns: %v", table, e)
		}
		snapshot = append(snapshot, table+"="+rows)
	}
	return strings.Join(snapshot, "\n")
}

type siteFile struct {
	data []byte
	mode os.FileMode
}

func snapshotSiteFiles(t *testing.T, root string) map[string]siteFile {
	t.Helper()
	result := map[string]siteFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		result[rel] = siteFile{content, info.Mode().Perm()}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot site files: %v", err)
	}
	return result
}
func equalFileSnapshots(a, b map[string]siteFile) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || v.mode != w.mode || !bytes.Equal(v.data, w.data) {
			return false
		}
	}
	return true
}
func cloneSite(t *testing.T, root string) string {
	t.Helper()
	dest := t.TempDir()
	if err := filepath.WalkDir(root, func(path string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dest, rel)
		if e.IsDir() {
			return os.MkdirAll(to, 0700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	return dest
}
func cloneSiteWithoutKey(t *testing.T, root string) string {
	t.Helper()
	dest := cloneSite(t, root)
	if err := os.Remove(filepath.Join(dest, "data", "auth-secrets.json")); err != nil {
		t.Fatal(err)
	}
	return dest
}
func expectCLIJSON(t *testing.T, r cliResult, label string) {
	t.Helper()
	if r.code != 0 {
		if strings.Contains(r.stderr, "Recovery operation is not implemented") {
			t.Fatalf("%s feature RED: legal operation is not implemented", label)
		}
		t.Fatalf("%s returned exit code %d, want 0", label, r.code)
	}
	if strings.Contains(r.stderr, "grant") || strings.Contains(r.stderr, testPGPass) {
		t.Fatalf("%s leaked grant/secret to stderr: %q", label, r.stderr)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("%s must be one JSON object (decode error: %T)", label, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("%s emitted more than one JSON value: %q", label, r.stdout)
	}
	if len(obj) == 0 {
		t.Fatalf("%s emitted empty JSON object", label)
	}
}
func expectSafeFailure(t *testing.T, r cliResult, label string, secrets []string) {
	t.Helper()
	if r.code != 1 {
		t.Fatalf("%s returned exit code %d, want 1", label, r.code)
	}
	if r.stdout != "" {
		t.Errorf("%s wrote %d bytes to stdout; operational failures must leave stdout empty", label, len(r.stdout))
	}
	if r.stderr == "" {
		t.Errorf("%s wrote no general error to stderr", label)
	}
	if strings.Contains(r.stderr, "Recovery operation is not implemented") {
		t.Fatalf("%s hit unimplemented stub instead of its failure condition", label)
	}
	if strings.Contains(r.stdout, "grant") || strings.Contains(r.stderr, "grant") {
		t.Errorf("%s exposed grant", label)
	}
	for i, s := range secrets {
		if s != "" && strings.Contains(r.stdout, s) {
			t.Errorf("%s leaked configured sentinel %d to stdout", label, i+1)
		}
		if s != "" && strings.Contains(r.stderr, s) {
			t.Errorf("%s leaked configured sentinel %d to stderr", label, i+1)
		}
	}
}
func mustTime(t *testing.T, v any) time.Time {
	t.Helper()
	x, e := time.Parse(time.RFC3339, fmt.Sprint(v))
	if e != nil {
		t.Fatalf("parse RFC3339 expires_at %v: %v", v, e)
	}
	return x
}
func otherProvider(p string) string {
	if p == "google" {
		return "github"
	}
	return "google"
}
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db == nil {
		return
	}
	sqlDB, e := db.DB()
	if e == nil {
		_ = sqlDB.Close()
	}
}
func treeNames(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	return out
}
