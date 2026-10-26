package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func pprArchive(t *testing.T, id string, permissions []string, mainJS string) []byte {
	t.Helper()
	manifest := map[string]any{"id": id, "name": "Preview fixture", "version": "1.0.0", "entrypoint": "main.js"}
	if len(permissions) > 0 {
		items := make([]map[string]string, 0, len(permissions))
		for _, permission := range permissions {
			items = append(items, map[string]string{"name": permission})
		}
		manifest["permissions"] = items
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal plugin manifest: %v", err)
	}
	return secZip(t, secZipEntry{name: "plugin.json", body: raw}, secZipEntry{name: "main.js", body: []byte(mainJS)})
}

func pprHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func pprPermissionHash(names ...string) string {
	// Fixtures pass the names in their canonical sorted order, matching the
	// permission approval contract's newline-delimited name list.
	return pprHash([]byte(strings.Join(names, "\n")))
}

func pprUpload(h *accessHarness, token, field, path, filename string, archive []byte, values map[string]string) accessResult {
	h.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		h.t.Fatalf("create %s upload field: %v", field, err)
	}
	if _, err := part.Write(archive); err != nil {
		h.t.Fatalf("write plugin archive: %v", err)
	}
	for key, value := range values {
		if err := w.WriteField(key, value); err != nil {
			h.t.Fatalf("write plugin form field %s: %v", key, err)
		}
	}
	if err := w.Close(); err != nil {
		h.t.Fatalf("close multipart plugin upload: %v", err)
	}
	return h.requestReader(http.MethodPost, path, &body, token, "application/json", w.FormDataContentType())
}

func pprPreviewFields(t *testing.T, body string, wantID string, wantPermissions []string, wantPermissionsHash, wantSHA256 string) {
	t.Helper()
	var got struct {
		ID              string   `json:"id"`
		Version         string   `json:"version"`
		Permissions     []string `json:"permissions"`
		PermissionsHash string   `json:"permissions_hash"`
		SHA256          string   `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("preview response is not JSON: %v: %s", err, body)
	}
	if got.ID != wantID || got.Version != "1.0.0" || strings.Join(got.Permissions, ",") != strings.Join(wantPermissions, ",") || got.PermissionsHash != wantPermissionsHash || got.SHA256 != wantSHA256 {
		t.Errorf("preview identity/approval fields = %+v, want id=%q version=1.0.0 permissions=%v permissions_hash=%q sha256=%q", got, wantID, wantPermissions, wantPermissionsHash, wantSHA256)
	}
}

func TestPluginPreviewRegressionUploadIsReadOnlyAndPreservesFileFieldAliases(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-upload-admin")
	token := h.token(admin)
	permissions := []string{"http.fetch", "store"}
	archive := pprArchive(t, "preview-upload", permissions, "preview upload")
	wantHash := pprPermissionHash(permissions...)
	wantSHA := pprHash(archive)
	beforeTree := secTreeSnapshot(t)
	var rowsBefore int64
	if err := h.db.Raw("SELECT count(*) FROM plugins").Scan(&rowsBefore).Error; err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"file", "plugin"} {
		t.Run("multipart field "+field, func(t *testing.T) {
			got := pprUpload(h, token, field, "/api/admin/plugins/upload?dry=1", "preview.zip", archive, nil)
			if got.code != http.StatusOK {
				t.Fatalf("upload preview using %s = %d, want 200: %s", field, got.code, got.body)
			}
			pprPreviewFields(t, got.body, "preview-upload", permissions, wantHash, wantSHA)
		})
	}
	if after := secTreeSnapshot(t); after != beforeTree {
		t.Errorf("dry upload preview changed files:\nbefore:\n%s\nafter:\n%s", beforeTree, after)
	}
	var rowsAfter int64
	if err := h.db.Raw("SELECT count(*) FROM plugins").Scan(&rowsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Errorf("dry upload preview changed plugin rows from %d to %d", rowsBefore, rowsAfter)
	}

	for name, values := range map[string]map[string]string{
		"missing acceptance":    nil,
		"wrong permission hash": {"accept_permissions": strings.Repeat("0", 64), "sha256": wantSHA},
		"wrong archive hash":    {"accept_permissions": wantHash, "sha256": strings.Repeat("0", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			got := pprUpload(h, token, "file", "/api/admin/plugins/upload", "preview.zip", archive, values)
			if got.code != http.StatusConflict {
				t.Errorf("upload with %s = %d, want 409: %s", name, got.code, got.body)
			}
		})
	}
	changed := pprArchive(t, "preview-upload", permissions, "changed after preview")
	got := pprUpload(h, token, "file", "/api/admin/plugins/upload", "preview.zip", changed, map[string]string{"accept_permissions": wantHash, "sha256": wantSHA})
	if got.code != http.StatusConflict {
		t.Errorf("upload whose bytes changed after preview = %d, want 409: %s", got.code, got.body)
	}
	installed := pprUpload(h, token, "file", "/api/admin/plugins/upload", "preview.zip", archive, map[string]string{"accept_permissions": wantHash, "sha256": wantSHA})
	if installed.code != http.StatusOK {
		t.Fatalf("upload with exact accepted permission list and preview hash = %d, want 200: %s", installed.code, installed.body)
	}
	if n := impCount(t, h, "SELECT count(*) FROM plugins WHERE id = 'preview-upload'"); n != 1 {
		t.Errorf("accepted upload created %d plugin rows, want one", n)
	}
	mainJS, err := os.ReadFile(filepath.Join(pluginDirRoot, "preview-upload", "main.js"))
	if err != nil || string(mainJS) != "preview upload" {
		t.Errorf("accepted archive content = %q, err %v", mainJS, err)
	}
}

func TestPluginPreviewRegressionURLPreviewAndAcceptanceBindExactDownloadedBytes(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-url-admin")
	token := h.token(admin)
	permissions := []string{"http.fetch", "store"}
	archive := pprArchive(t, "preview-url", permissions, "url preview")
	currentArchive := archive
	var archiveMu sync.RWMutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		archiveMu.RLock()
		responseBody := bytes.Clone(currentArchive)
		archiveMu.RUnlock()
		_, _ = w.Write(responseBody)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	url := srv.URL + "/plugin.zip"
	beforeTree := secTreeSnapshot(t)
	var rowsBefore int64
	if err := h.db.Raw("SELECT count(*) FROM plugins").Scan(&rowsBefore).Error; err != nil {
		t.Fatal(err)
	}
	wantHash := pprPermissionHash(permissions...)
	wantSHA := pprHash(archive)
	post := func(body string) accessResult {
		return s08bInteractive(h, http.MethodPost, "/api/admin/plugins/install-url", strings.NewReader(body), token, "application/json")
	}
	// The preview switch is a query parameter; use the request helper directly
	// to keep the URL's approval mode visible in this assertion.
	preview := s08bInteractive(h, http.MethodPost, "/api/admin/plugins/install-url?dry=1", strings.NewReader(fmt.Sprintf(`{"url":%q}`, url)), token, "application/json")
	if preview.code != http.StatusOK {
		t.Fatalf("URL preview = %d, want 200: %s", preview.code, preview.body)
	}
	pprPreviewFields(t, preview.body, "preview-url", permissions, wantHash, wantSHA)
	if after := secTreeSnapshot(t); after != beforeTree {
		t.Errorf("dry URL preview changed files:\nbefore:\n%s\nafter:\n%s", beforeTree, after)
	}
	var rowsAfter int64
	if err := h.db.Raw("SELECT count(*) FROM plugins").Scan(&rowsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Errorf("dry URL preview changed plugin rows from %d to %d", rowsBefore, rowsAfter)
	}

	base := fmt.Sprintf(`{"url":%q,"accept_permissions":%q,"sha256":%q}`, url, wantHash, wantSHA)
	if got := post(fmt.Sprintf(`{"url":%q}`, url)); got.code != http.StatusConflict {
		t.Errorf("URL install without permission acceptance = %d, want 409: %s", got.code, got.body)
	}
	wrong := fmt.Sprintf(`{"url":%q,"accept_permissions":%q,"sha256":%q}`, url, strings.Repeat("0", 64), wantSHA)
	if got := post(wrong); got.code != http.StatusConflict {
		t.Errorf("URL install with wrong permission hash = %d, want 409: %s", got.code, got.body)
	}
	archiveMu.Lock()
	currentArchive = pprArchive(t, "preview-url", permissions, "changed after URL preview")
	archiveMu.Unlock()
	if got := post(base); got.code != http.StatusBadRequest {
		t.Errorf("URL archive changed after sha256 approval = %d, want 400 mismatch: %s", got.code, got.body)
	}
	archiveMu.Lock()
	currentArchive = archive
	archiveMu.Unlock()
	if got := post(base); got.code != http.StatusOK {
		t.Fatalf("URL install with exact preview sha256 and permission hash = %d, want 200: %s", got.code, got.body)
	}
	if n := impCount(t, h, "SELECT count(*) FROM plugins WHERE id = 'preview-url'"); n != 1 {
		t.Errorf("accepted URL install created %d plugin rows, want one", n)
	}
}

func TestPluginPreviewRegressionAcceptsWrapperWithExplicitAssetDirectory(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-assets-admin")
	token := h.token(admin)
	manifest := secManifest(t, "preview-assets")
	archive := secZip(t,
		secZipEntry{name: "release/plugin.json", body: manifest},
		secZipEntry{name: "release/main.js", body: []byte("asset fixture")},
		secZipEntry{name: "release/assets/"},
		secZipEntry{name: "release/assets/style.css", body: []byte(".fixture { color: red }\n")},
	)
	before := secTreeSnapshot(t)
	got := pprUpload(h, token, "file", "/api/admin/plugins/upload?dry=1", "assets.zip", archive, nil)
	if got.code != http.StatusOK {
		t.Fatalf("valid one-wrapper archive with an explicit assets directory = %d, want 200: %s", got.code, got.body)
	}
	pprPreviewFields(t, got.body, "preview-assets", nil, pprPermissionHash(), pprHash(archive))
	if after := secTreeSnapshot(t); after != before {
		t.Errorf("valid dry preview with an explicit assets directory changed files:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestPluginPreviewRegressionRejectsAmbiguousOrMixedManifestLayouts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-layout-admin")
	token := h.token(admin)
	manifest := secManifest(t, "preview-layout")
	cases := map[string][]secZipEntry{
		"root and wrapper entries mixed": {
			{name: "wrapper/plugin.json", body: manifest},
			{name: "plugin.json", body: manifest},
			{name: "main.js", body: []byte("mixed")},
		},
		"multiple nested manifests": {
			{name: "one/plugin.json", body: manifest},
			{name: "two/plugin.json", body: manifest},
		},
		"manifest deeper than one wrapper": {{name: "one/two/plugin.json", body: manifest}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			before := secTreeSnapshot(t)
			archive := secZip(t, entries...)
			got := pprUpload(h, token, "file", "/api/admin/plugins/upload?dry=1", "bad.zip", archive, nil)
			if got.code != http.StatusBadRequest {
				t.Errorf("malformed archive preview = %d, want 400: %s", got.code, got.body)
			}
			if after := secTreeSnapshot(t); after != before {
				t.Errorf("rejected preview changed files:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestPluginPreviewRegressionEmptyPermissionLegacyUploadStillInstalls(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-legacy-admin")
	token := h.token(admin)
	archive := pprArchive(t, "preview-legacy", nil, "legacy upload")
	got := pprUpload(h, token, "plugin", "/api/admin/plugins/upload", "legacy.zip", archive, nil)
	if got.code != http.StatusOK {
		t.Fatalf("legacy upload with no declared permissions or acceptance fields = %d, want 200: %s", got.code, got.body)
	}
	if n := impCount(t, h, "SELECT count(*) FROM plugins WHERE id = 'preview-legacy'"); n != 1 {
		t.Errorf("legacy upload created %d plugin rows, want one", n)
	}
}

func TestPluginPreviewRegressionSameManifestReplacementDBFailurePreservesOldFiles(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plugin-preview-replacement-admin")
	token := h.token(admin)
	const id = "preview-replacement"
	manifest := secManifest(t, id)
	oldArchive := secZip(t, secZipEntry{name: "plugin.json", body: manifest}, secZipEntry{name: "main.js", body: []byte("old implementation")})
	if got := secUploadPlugin(t, h, token, "old.zip", oldArchive); got.code != http.StatusOK {
		t.Fatalf("initial install = %d, want 200: %s", got.code, got.body)
	}
	if err := h.db.Exec(`CREATE FUNCTION jelly_test_reject_same_manifest_plugin_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected plugin row failure'; END; $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER jelly_test_reject_same_manifest_plugin_write BEFORE INSERT OR UPDATE ON plugins FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_same_manifest_plugin_write()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_same_manifest_plugin_write ON plugins`)
		_ = h.db.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_same_manifest_plugin_write()`)
	})
	newArchive := secZip(t, secZipEntry{name: "plugin.json", body: manifest}, secZipEntry{name: "main.js", body: []byte("new implementation")})
	got := secUploadPlugin(t, h, token, "new.zip", newArchive)
	if got.code < 400 {
		t.Fatalf("same-version, same-manifest replacement with rejected DB update = %d, want error: %s", got.code, got.body)
	}
	mainJS, err := os.ReadFile(filepath.Join(pluginDirRoot, id, "main.js"))
	if err != nil || string(mainJS) != "old implementation" {
		t.Errorf("old entrypoint after rejected DB update = %q, err %v", mainJS, err)
	}
	storedManifest, err := os.ReadFile(filepath.Join(pluginDirRoot, id, "plugin.json"))
	if err != nil || !bytes.Equal(storedManifest, manifest) {
		t.Errorf("old manifest after rejected DB update = %q, err %v", storedManifest, err)
	}
	var version string
	if err := h.db.Raw("SELECT version FROM plugins WHERE id = ?", id).Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != "1.0.0" {
		t.Errorf("plugin row version after rejected same-version replacement = %q, want 1.0.0", version)
	}
}

func TestPluginPreviewRegressionRecoveryResolvesPreparedAndCommittedSwaps(t *testing.T) {
	for _, state := range []string{"prepared-before-rename", "prepared-after-rename", "committed-tree-match"} {
		t.Run(state, func(t *testing.T) {
			h := newAccessHarness(t)
			id := "recovery-" + strings.ReplaceAll(state, "-", "")
			admin := h.user("admin", "plugin-preview-recovery-"+strings.ReplaceAll(state, "-", ""))
			oldArchive := pprArchive(t, id, nil, "old implementation")
			if got := secUploadPlugin(t, h, h.token(admin), "old.zip", oldArchive); got.code != http.StatusOK {
				t.Fatalf("initial plugin install = %d, want 200: %s", got.code, got.body)
			}
			const sentinelID = "recovery-unrelated-sentinel"
			if err := os.MkdirAll(filepath.Join(pluginDirRoot, sentinelID), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDirRoot, sentinelID, "keep.txt"), []byte("unrelated plugin"), 0o644); err != nil {
				t.Fatal(err)
			}
			baseline := secTreeSnapshot(t)

			stageName := ".jelly-plugin-stage-" + id
			stagePath := pluginTransactionPath(stageName)
			if err := os.Mkdir(stagePath, 0o755); err != nil {
				t.Fatalf("create staged replacement: %v", err)
			}
			newManifest := bytes.Replace(secManifest(t, id), []byte(`"1.0.0"`), []byte(`"2.0.0"`), 1)
			if err := os.WriteFile(filepath.Join(stagePath, "plugin.json"), newManifest, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stagePath, "main.js"), []byte("new implementation"), 0o644); err != nil {
				t.Fatal(err)
			}
			meta, manifestDigest, err := pluginManifestDigest(stagePath)
			if err != nil {
				t.Fatalf("read staged manifest: %v", err)
			}
			treeDigest, err := pluginTreeDigest(stagePath)
			if err != nil {
				t.Fatalf("digest staged plugin tree: %v", err)
			}
			if meta.Version != "2.0.0" {
				t.Fatalf("staged version = %q, want 2.0.0", meta.Version)
			}
			stamp := time.Now().UTC().Truncate(time.Microsecond)
			permissionsHash := pprPermissionHash()
			journal := pluginInstallJournal{
				Action: "install", ID: id, Version: meta.Version,
				PermissionsHash: permissionsHash, ManifestSHA256: manifestDigest, TreeSHA256: treeDigest,
				CommitStamp: stamp, Stage: stageName,
				Backup: fmt.Sprintf(".jelly-plugin-install-%s-%d", id, time.Now().UnixNano()), Phase: "prepared",
			}
			journalPath := pluginInstallJournalPath(id)
			if err := writePluginInstallJournal(journalPath, journal); err != nil {
				t.Fatalf("persist prepared journal: %v", err)
			}

			if state != "prepared-before-rename" {
				if err := os.Rename(filepath.Join(pluginDirRoot, id), pluginTransactionPath(journal.Backup)); err != nil {
					t.Fatalf("move old plugin to backup: %v", err)
				}
				if err := os.Rename(stagePath, filepath.Join(pluginDirRoot, id)); err != nil {
					t.Fatalf("activate staged replacement: %v", err)
				}
				if state == "committed-tree-match" {
					journal.Phase = "swapped"
				}
				if err := writePluginInstallJournal(journalPath, journal); err != nil {
					t.Fatalf("persist post-rename journal: %v", err)
				}
			}
			if state == "committed-tree-match" {
				if err := h.db.Exec("UPDATE plugins SET version = ?, permissions_granted_hash = ?, updated_at = ? WHERE id = ?", meta.Version, permissionsHash, stamp, id).Error; err != nil {
					t.Fatalf("record matching committed plugin row: %v", err)
				}
			}

			if err := recoverPluginInstallJournals(); err != nil {
				t.Fatalf("recover %s journal: %v", state, err)
			}
			wantMain := "old implementation"
			wantVersion := "1.0.0"
			if state == "committed-tree-match" {
				wantMain = "new implementation"
				wantVersion = "2.0.0"
			}
			mainJS, err := os.ReadFile(filepath.Join(pluginDirRoot, id, "main.js"))
			if err != nil || string(mainJS) != wantMain {
				t.Errorf("recovered %s entrypoint = %q, err %v, want %q", state, mainJS, err, wantMain)
			}
			var gotVersion string
			if err := h.db.Raw("SELECT version FROM plugins WHERE id = ?", id).Scan(&gotVersion).Error; err != nil {
				t.Fatal(err)
			}
			if gotVersion != wantVersion {
				t.Errorf("recovered %s plugin row version = %q, want %q", state, gotVersion, wantVersion)
			}
			if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
				t.Errorf("recovery left journal behind: stat error %v", err)
			}
			if _, err := os.Stat(stagePath); !os.IsNotExist(err) {
				t.Errorf("recovery left stage directory behind: stat error %v", err)
			}
			if _, err := os.Stat(pluginTransactionPath(journal.Backup)); !os.IsNotExist(err) {
				t.Errorf("recovery left backup directory behind: stat error %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(pluginDirRoot, sentinelID, "keep.txt")); err != nil || string(content) != "unrelated plugin" {
				t.Errorf("recovery changed unrelated plugin sentinel to %q, err %v", content, err)
			}
			if state == "committed-tree-match" {
				if digest, err := pluginTreeDigest(filepath.Join(pluginDirRoot, id)); err != nil || digest != treeDigest {
					t.Errorf("committed recovery tree digest = %q, err %v, want %q", digest, err, treeDigest)
				}
			} else if after := secTreeSnapshot(t); after != baseline {
				t.Errorf("rollback recovery did not restore the filesystem for %s:\nbefore:\n%s\nafter:\n%s", state, baseline, after)
			}
		})
	}
}

func TestPluginPreviewRegressionInvalidJournalCleanupPreservesFiles(t *testing.T) {
	_ = newAccessHarness(t)
	if err := os.MkdirAll(filepath.Join(pluginDirRoot, "journal-sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDirRoot, "journal-sentinel", "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("data", ".jelly-plugin-install-journal-sentinel-keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", ".jelly-plugin-install-journal-sentinel-keep", "keep.txt"), []byte("backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := secTreeSnapshot(t)
	if err := finishPluginArchiveCommit(pluginInstallJournal{}); err == nil {
		t.Error("finishPluginArchiveCommit accepted a zero-value journal")
	}
	if err := rollbackPluginArchive(pluginInstallJournal{}); err == nil {
		t.Error("rollbackPluginArchive accepted a zero-value journal")
	}
	invalid := pluginInstallJournal{Journaled: true, Action: "install", ID: "journal-sentinel", Phase: "swapped", Stage: "../outside", Backup: "../outside"}
	if err := finishPluginArchiveCommit(invalid); err == nil {
		t.Error("finishPluginArchiveCommit accepted journal paths outside the plugin transaction directory")
	}
	if err := rollbackPluginArchive(invalid); err == nil {
		t.Error("rollbackPluginArchive accepted journal paths outside the plugin transaction directory")
	}
	if after := secTreeSnapshot(t); after != before {
		t.Errorf("invalid journal cleanup changed files:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	journalPath := pluginInstallJournalPath("journal-sentinel")
	if err := os.WriteFile(journalPath, []byte(`{"action":"install","id":"journal-sentinel","phase":"swapped"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRecovery := secTreeSnapshot(t)
	if err := recoverPluginInstallJournals(); err == nil {
		t.Fatal("recovery accepted an invalid persisted plugin journal")
	}
	if after := secTreeSnapshot(t); after != beforeRecovery {
		t.Errorf("failed closed journal recovery changed files:\nbefore:\n%s\nafter:\n%s", beforeRecovery, after)
	}
}
