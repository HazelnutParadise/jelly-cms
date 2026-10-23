package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// bkpCall sends a request either with the admin session cookie (jwt_token,
// the interactive path) or with an Authorization: Bearer header.
func bkpCall(h *accessHarness, method, path, body, token string, bearer bool) accessResult {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if token != "" {
		if bearer {
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
		} else {
			req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
		}
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

// bkpRow is the subset of a backups row these tests read.
type bkpRow struct {
	ID       int64  `gorm:"column:id"`
	State    string `gorm:"column:state"`
	Kind     string `gorm:"column:kind"`
	FilePath string `gorm:"column:file_path"`
	SHA256   string `gorm:"column:sha256"`
	Manifest string `gorm:"column:manifest"`
}

func bkpLatest(t *testing.T, h *accessHarness) bkpRow {
	t.Helper()
	var row bkpRow
	err := h.db.Raw(`SELECT id, state, kind, COALESCE(file_path,'') AS file_path, COALESCE(sha256,'') AS sha256,
		COALESCE(manifest::text,'') AS manifest FROM backups ORDER BY id DESC LIMIT 1`).Scan(&row).Error
	if err != nil || row.ID == 0 {
		t.Fatalf("read newest backup row: id=%d err=%v", row.ID, err)
	}
	return row
}

// bkpSeed writes an archive-shaped file under data/backups (0600) and a
// matching done row, so download and restore paths can be exercised without
// depending on the backup job.
func bkpSeed(t *testing.T, h *accessHarness, kind string, created time.Time, content []byte) bkpRow {
	t.Helper()
	if err := os.MkdirAll("data/backups", 0o700); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("data/backups/seed-%d.bak", time.Now().UnixNano())
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	var row bkpRow
	err := h.db.Raw(`INSERT INTO backups (state, kind, contents, file_path, size_bytes, sha256, manifest, created_at, finished_at)
		VALUES ('done', ?, '["db"]'::jsonb, ?, ?, ?, '{}'::jsonb, ?, ?) RETURNING id, state, kind, file_path, sha256`,
		kind, name, int64(len(content)), hex.EncodeToString(sum[:]), created, created).Scan(&row).Error
	if err != nil || row.ID == 0 {
		t.Fatalf("insert backup row: %v", err)
	}
	return row
}

func bkpFindKey(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val, true
		}
		for _, child := range x {
			if found, ok := bkpFindKey(child, key); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range x {
			if found, ok := bkpFindKey(child, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func bkpJSONID(t *testing.T, body string) int64 {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	id, ok := bkpFindKey(v, "id")
	num, isNum := id.(float64)
	if !ok || !isNum {
		t.Fatalf("response has no numeric id: %s", body)
	}
	return int64(num)
}

func TestBackupRunCreatesEncryptedArchiveWithManifest(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-run-admin")
	const marker = "BKP-PLAINTEXT-MARKER-7f3a"
	if err := h.db.Create(&core.Post{Title: "Marker", Slug: "bkp-marker", Content: marker, Type: "post", Status: "published", AuthorID: admin.ID}).Error; err != nil {
		t.Fatal(err)
	}

	// The test queue runs the job inline, so the row is final after the 202.
	res := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db","settings"],"note":"run"}`, h.token(admin), false)
	if res.code != http.StatusAccepted {
		t.Fatalf("POST /api/admin/backups = %d, want 202: %s", res.code, res.body)
	}
	row := bkpLatest(t, h)
	if row.State != "done" || row.Kind != "manual" {
		t.Fatalf("backup row state=%q kind=%q, want done/manual", row.State, row.Kind)
	}
	if !strings.HasPrefix(filepath.ToSlash(filepath.Clean(row.FilePath)), "data/backups/") {
		t.Fatalf("archive path %q is not under data/backups", row.FilePath)
	}
	if strings.Contains(row.FilePath, "uploads") {
		t.Fatalf("archive path %q is under uploads", row.FilePath)
	}

	dirInfo, err := os.Stat("data/backups")
	if err != nil {
		t.Fatalf("stat data/backups: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("data/backups mode = %o, want 0700", perm)
	}
	fileInfo, err := os.Stat(row.FilePath)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("archive mode = %o, want 0600", perm)
	}

	raw, err := os.ReadFile(row.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != row.SHA256 {
		t.Errorf("sha256 of archive = %s, row says %s", got, row.SHA256)
	}
	if bytes.Contains(raw, []byte(marker)) {
		t.Error("archive contains plaintext content; it must be AES-GCM encrypted")
	}
	if bytes.Contains(raw, []byte(`"slug":"bkp-marker"`)) {
		t.Error("archive contains plaintext JSONL rows")
	}

	var manifest backup.Manifest
	if err := json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	for _, table := range []string{"posts", "users"} {
		if _, ok := manifest.Tables[table]; !ok {
			t.Errorf("manifest.tables lacks %q; have %d tables", table, len(manifest.Tables))
		}
	}
	if manifest.Tables["posts"].SHA256 == "" || manifest.Tables["posts"].Rows < 1 {
		t.Errorf("posts table entry = %+v, want rows >= 1 and a sha256", manifest.Tables["posts"])
	}
}

func TestBackupDownloadInteractiveOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-dl-admin")
	editor := h.user("editor", "bkp-dl-editor")
	row := bkpSeed(t, h, "manual", time.Now(), []byte("ciphertext-placeholder"))
	path := fmt.Sprintf("/api/admin/backups/%d/download", row.ID)

	for _, token := range []string{"jly_x", h.token(admin)} {
		res := bkpCall(h, http.MethodGet, path, "", token, true)
		if res.code != http.StatusUnauthorized && res.code != http.StatusForbidden {
			t.Errorf("Bearer download = %d, want 401 or 403", res.code)
		}
	}
	if res := bkpCall(h, http.MethodGet, path, "", h.token(admin), false); res.code != http.StatusOK {
		t.Errorf("admin session download = %d, want 200: %s", res.code, res.body)
	}
	if res := bkpCall(h, http.MethodGet, path, "", h.token(editor), false); res.code != http.StatusForbidden {
		t.Errorf("editor session download = %d, want 403", res.code)
	}
}

func TestBackupRestoreRequiresConfirmAndMakesPreRestore(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-restore-admin")
	post := core.Post{Title: "Keep", Slug: "bkp-keep", Content: "kept", Type: "post", Status: "published", AuthorID: admin.ID}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	if res := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db"],"note":"before delete"}`, h.token(admin), false); res.code != http.StatusAccepted {
		t.Fatalf("create backup = %d, want 202: %s", res.code, res.body)
	}
	backupID := bkpLatest(t, h).ID
	restorePath := fmt.Sprintf("/api/admin/backups/%d/restore", backupID)

	if err := h.db.Unscoped().Delete(&core.Post{}, post.ID).Error; err != nil {
		t.Fatal(err)
	}

	if res := bkpCall(h, http.MethodPost, restorePath, `{}`, h.token(admin), false); res.code != http.StatusBadRequest {
		t.Fatalf("restore without confirm = %d, want 400", res.code)
	}
	var remaining int64
	h.db.Raw("SELECT COUNT(*) FROM posts WHERE slug = 'bkp-keep'").Scan(&remaining)
	if remaining != 0 {
		t.Fatal("a restore without confirmation changed data")
	}

	res := bkpCall(h, http.MethodPost, restorePath, `{"confirm":"RESTORE"}`, h.token(admin), false)
	if res.code != http.StatusOK && res.code != http.StatusAccepted {
		t.Fatalf("restore with confirm = %d, want 2xx: %s", res.code, res.body)
	}

	var preRestore int64
	h.db.Raw("SELECT COUNT(*) FROM backups WHERE kind = 'pre_restore' AND state = 'done'").Scan(&preRestore)
	if preRestore != 1 {
		t.Errorf("pre_restore backups = %d, want 1", preRestore)
	}
	var maintenanceStarted string
	h.db.Raw("SELECT COALESCE(value,'') FROM options WHERE key = 'maintenance_started_at'").Scan(&maintenanceStarted)
	if maintenanceStarted == "" {
		t.Error("maintenance_started_at not recorded; restore must enable maintenance mode")
	}

	var restored int64
	h.db.Raw("SELECT COUNT(*) FROM posts WHERE slug = 'bkp-keep'").Scan(&restored)
	if restored != 1 {
		t.Errorf("deleted post not restored: count=%d", restored)
	}
}

func TestBackupRestoreRejectsTamperedOrZipSlip(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-tamper-admin")
	token := h.token(admin)

	t.Run("checksum mismatch", func(t *testing.T) {
		if res := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db"]}`, token, false); res.code != http.StatusAccepted {
			t.Fatalf("create backup = %d, want 202", res.code)
		}
		row := bkpLatest(t, h)
		raw, err := os.ReadFile(row.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 {
			t.Fatal("archive is empty")
		}
		raw[len(raw)/2] ^= 0x01
		if err := os.WriteFile(row.FilePath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		res := bkpCall(h, http.MethodPost, fmt.Sprintf("/api/admin/backups/%d/restore", row.ID), `{"confirm":"RESTORE"}`, token, false)
		if res.code != http.StatusBadRequest {
			t.Errorf("restore of tampered archive = %d, want 400", res.code)
		}
	})

	t.Run("zip slip entry", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("../x")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("escaped"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		row := bkpSeed(t, h, "manual", time.Now(), buf.Bytes())
		res := bkpCall(h, http.MethodPost, fmt.Sprintf("/api/admin/backups/%d/restore", row.ID), `{"confirm":"RESTORE"}`, token, false)
		if res.code != http.StatusBadRequest {
			t.Errorf("restore with ../ entry = %d, want 400", res.code)
		}
		for _, p := range []string{"x", "data/x", "data/backups/x"} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("restore wrote %s", p)
			}
		}
	})
}

func TestBackupScheduleAndPrune(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-prune-admin")
	token := h.token(admin)

	res := bkpCall(h, http.MethodPut, "/api/admin/backups/schedule", `{"enabled":true,"time":"03:00","keep_daily":2}`, token, false)
	if res.code != http.StatusOK {
		t.Fatalf("PUT schedule = %d, want 200: %s", res.code, res.body)
	}

	now := time.Now().UTC()
	oldest := bkpSeed(t, h, "scheduled", now.Add(-48*time.Hour), []byte("day-2"))
	middle := bkpSeed(t, h, "scheduled", now.Add(-24*time.Hour), []byte("day-1"))
	third := bkpSeed(t, h, "scheduled", now, []byte("day-0"))

	if err := backup.Prune(context.Background()); err != nil {
		t.Fatalf("backup.Prune: %v", err)
	}

	var kept []int64
	h.db.Raw("SELECT id FROM backups WHERE kind = 'scheduled' AND state = 'done' ORDER BY id").Scan(&kept)
	if len(kept) != 2 || kept[0] != middle.ID || kept[1] != third.ID {
		t.Errorf("scheduled backups after prune = %v, want [%d %d] (oldest %d removed)", kept, middle.ID, third.ID, oldest.ID)
	}
	if _, err := os.Stat(oldest.FilePath); err == nil {
		t.Errorf("pruned archive %s still on disk", oldest.FilePath)
	}
}

func TestBackupExcludesConfigAndSecretsWithoutPassphrase(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-secret-admin")
	token := h.token(admin)

	const configSecret = "CFG-SECRET-db-password-91"
	const keyMaterial = "AUTH-KEY-MATERIAL-55"
	if err := os.WriteFile("config.json", []byte(`{"database":{"password":"`+configSecret+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/auth-secrets.json", []byte(`{"key":"`+keyMaterial+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if res := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db","settings"]}`, token, false); res.code != http.StatusAccepted {
		t.Fatalf("create backup without passphrase = %d, want 202: %s", res.code, res.body)
	}
	row := bkpLatest(t, h)
	raw, err := os.ReadFile(row.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(configSecret)) || bytes.Contains(raw, []byte(keyMaterial)) {
		t.Error("archive contains config or key material in plaintext")
	}
	if strings.Contains(row.Manifest, "config.json") {
		t.Error("manifest lists config.json; config must never be archived")
	}
	if strings.Contains(row.Manifest, "auth-secrets.json") {
		t.Error("manifest lists auth-secrets.json without a passphrase")
	}

	res := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db","settings"],"passphrase":"correct-horse-battery-staple"}`, token, false)
	if res.code != http.StatusAccepted {
		t.Fatalf("create backup with passphrase = %d, want 202: %s", res.code, res.body)
	}
	withSecrets := bkpLatest(t, h)
	if !strings.Contains(withSecrets.Manifest, "auth-secrets.json") {
		t.Error("manifest does not list auth-secrets.json although a passphrase was given")
	}
	if strings.Contains(withSecrets.Manifest, "config.json") {
		t.Error("manifest lists config.json even with a passphrase")
	}
}

func TestExportMarkdownZipWxrJson(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "bkp-export-admin")
	token := h.token(admin)
	for _, p := range []core.Post{
		{Title: "Alpha Post", Slug: "alpha", Content: "<p>Alpha body</p>", Type: "post", Status: "published", AuthorID: admin.ID},
		{Title: "Beta Post", Slug: "beta", Content: "<p>Beta body</p>", Type: "post", Status: "published", AuthorID: admin.ID},
	} {
		p := p
		if err := h.db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	for _, format := range []string{"markdown_zip", "wxr", "json"} {
		res := bkpCall(h, http.MethodPost, "/api/admin/export", fmt.Sprintf(`{"format":%q}`, format), token, false)
		if res.code != http.StatusAccepted {
			t.Fatalf("POST /api/admin/export format=%s = %d, want 202: %s", format, res.code, res.body)
		}
		id := bkpJSONID(t, res.body)
		dl := bkpCall(h, http.MethodGet, fmt.Sprintf("/api/admin/exports/%d/download", id), "", token, false)
		if dl.code != http.StatusOK {
			t.Fatalf("download format=%s = %d, want 200", format, dl.code)
		}
		if cd := dl.header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("format=%s Content-Disposition = %q, want attachment", format, cd)
		}

		switch format {
		case "markdown_zip":
			zr, err := zip.NewReader(bytes.NewReader([]byte(dl.body)), int64(len(dl.body)))
			if err != nil {
				t.Fatalf("markdown export is not a zip: %v", err)
			}
			var md []string
			for _, f := range zr.File {
				if strings.HasSuffix(f.Name, ".md") {
					md = append(md, f.Name)
					rc, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(rc)
					rc.Close()
					if !strings.HasPrefix(string(body), "---\n") || !strings.Contains(string(body), "title:") {
						t.Errorf("%s lacks YAML front matter with title", f.Name)
					}
				}
			}
			if len(md) != 2 {
				t.Errorf("markdown zip has %d .md files, want one per post (2): %v", len(md), md)
			}
		case "wxr":
			if !strings.Contains(dl.body, "<rss") || !strings.Contains(dl.body, "alpha") {
				t.Errorf("WXR export is missing the rss root or the alpha post")
			}
		case "json":
			var v any
			if err := json.Unmarshal([]byte(dl.body), &v); err != nil {
				t.Errorf("JSON export is not valid JSON: %v", err)
			}
		}
	}
}
