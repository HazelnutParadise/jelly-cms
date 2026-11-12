package server

// Regression tests for the PR #53 fourth-round review fixes.
//
// TestPluginDisableUnloadsEvenWhenRequestIsCanceled is the contract that disabling
// a running plugin unloads it even when the request context is cancelled after
// the plugins row was already set to disabled. The request is held behind a row
// lock so that the cancellation lands between the UPDATE and the unload.
//
// TestPluginUploadRejectsMismatchedSHAForPermissionlessArchive is the contract
// that an upload which carries a sha256 field must match the archive it installs,
// even when the archive declares no permissions. A legacy upload without the
// sha256 field still installs.
//
// These tests are the contract; implementers must not edit this file. Every test
// needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container. All waits
// poll with a bounded loop; a fixed sleep is never followed directly by an
// assertion.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// TestPluginDisableUnloadsEvenWhenRequestIsCanceled covers the review fix that a
// disable whose request context is cancelled still unloads the plugin once its
// row says disabled.
func TestPluginDisableUnloadsEvenWhenRequestIsCanceled(t *testing.T) {
	h := newPluginReloadHarness(t)
	run := nextPluginReloadRun()
	admin := h.user("admin", "round4-disable-admin")
	token := h.token(admin)

	id := "round4-disable"
	dir := filepath.Join("data", "plugins", id)
	plgSeed(t, h.accessHarness, id, fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","entrypoint":"index.js"}`, id, id), true)
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(reloadHookScript(run, "v1", "data.n + 1")), 0o600); err != nil {
		t.Fatalf("write entrypoint for %q: %v", id, err)
	}
	h.loadPlugin(t, id)
	if !h.runtime.IsLoaded(id) {
		t.Fatalf("IsLoaded(%q) before disable = false, want true", id)
	}

	// Hold the plugins row in another transaction so the disable UPDATE waits
	// behind it. The request context is cancelled while that UPDATE waits, so
	// the cancellation lands after the row is about to change and before unload.
	tx := h.db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin row-lock transaction: %v", tx.Error)
	}
	defer func() { _ = tx.Rollback().Error }()
	if err := tx.Exec("SELECT id FROM plugins WHERE id = ? FOR UPDATE", id).Error; err != nil {
		t.Fatalf("lock plugins row %q: %v", id, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request := httptest.NewRequest(http.MethodPost, "/api/admin/plugins/"+id+"/disable", nil)
	setAccessToken(request, token)
	request.Header.Set("X-Jelly-CSRF", "1")
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request = request.WithContext(ctx)

	recorder := httptest.NewRecorder()
	result := make(chan any, 1)
	go func() {
		defer func() { result <- recover() }()
		h.echo.ServeHTTP(recorder, request)
	}()

	waitDeadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		if err := h.db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query LIKE 'UPDATE plugins SET enabled = FALSE%'`).Scan(&waiting).Error; err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("disable of %q never waited on the plugins row lock; the handler may no longer run its UPDATE before unload", id)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("release plugins row lock: %v", err)
	}

	select {
	case panicked := <-result:
		if panicked != nil {
			t.Fatalf("disable handler panicked: %v", panicked)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("disable of %q did not return after the row lock was released", id)
	}
	t.Logf("disable status with cancelled request context = %d; body = %s", recorder.Code, recorder.Body.String())

	var disabledRows int64
	if err := h.db.Raw("SELECT count(*) FROM plugins WHERE id = ? AND NOT enabled", id).Scan(&disabledRows).Error; err != nil {
		t.Fatalf("read plugins row for %q: %v", id, err)
	}
	if disabledRows != 1 {
		t.Fatalf("plugins rows for %q with enabled = false = %d, want 1; the disable UPDATE must have run", id, disabledRows)
	}

	unloadDeadline := time.Now().Add(5 * time.Second)
	for h.runtime.IsLoaded(id) {
		if time.Now().After(unloadDeadline) {
			t.Fatalf(
				"IsLoaded(%q) = true 5s after disable left plugins.enabled = false, want false; the runtime still runs a plugin the admin disabled (disable status %d, body %s)",
				id, recorder.Code, recorder.Body.String(),
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPluginUploadRejectsMismatchedSHAForPermissionlessArchive covers the review
// fix that an upload carrying a sha256 field is checked against the archive it
// installs, also for a plugin that declares no permissions.
func TestPluginUploadRejectsMismatchedSHAForPermissionlessArchive(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "round4-sha-admin")
	token := h.token(admin)

	const id = "round4-nopriv"
	archiveA := pprArchive(t, id, nil, "round4 version A")
	archiveB := pprArchive(t, id, nil, "round4 version B")
	shaA := pprHash(archiveA)
	if shaA == pprHash(archiveB) {
		t.Fatal("fixture archives A and B have the same sha256; the mismatch case needs different bytes")
	}

	got := pprUpload(h, token, "file", "/api/admin/plugins/upload", "nopriv-b.zip", archiveB, map[string]string{
		"accept_permissions": pprPermissionHash(),
		"sha256":             shaA,
	})
	if got.code != http.StatusConflict {
		t.Errorf("upload of archive B with archive A's sha256 = %d, want 409: %s", got.code, got.body)
	}
	if _, err := os.Stat(filepath.Join("data", "plugins", id)); !os.IsNotExist(err) {
		t.Errorf("data/plugins/%s after the rejected upload: stat err = %v, want not exist; archive B must not be installed", id, err)
	}
	var rows int64
	if err := h.db.Raw("SELECT count(*) FROM plugins WHERE id = ?", id).Scan(&rows).Error; err != nil {
		t.Fatalf("count plugins rows for %q: %v", id, err)
	}
	if rows != 0 {
		t.Errorf("plugins rows for %q after the rejected upload = %d, want 0", id, rows)
	}

	const legacyID = "round4-legacy-nopriv"
	legacy := pprArchive(t, legacyID, nil, "round4 legacy")
	legacyGot := pprUpload(h, token, "file", "/api/admin/plugins/upload", "legacy.zip", legacy, nil)
	if legacyGot.code != http.StatusOK {
		t.Errorf("legacy upload without a sha256 field = %d, want 200: %s", legacyGot.code, legacyGot.body)
	}
}
