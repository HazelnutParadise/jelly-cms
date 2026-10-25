package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/HazelnutParadise/jelly-cms/internal/update"
)

func updatePrebackupFixture(t *testing.T, assets map[string][]byte, signer updSigner) (string, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	updSetVersion(t, "1.0.0")
	if err := os.MkdirAll("web", 0o755); err != nil {
		t.Fatal(err)
	}
	webIndex := filepath.Join("web", "index.html")
	if err := os.WriteFile(webIndex, []byte("old web tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := updExecutable(t, "old executable")
	fake := updNewFakeGitHub(t, updRelease{Tag: "v1.2.0", Assets: assets})
	updSeams(t, fake, signer, exe)
	return exe, webIndex
}

func TestUpdatePrebackupRegressionHookRunsAfterValidationBeforeSwap(t *testing.T) {
	signer := updNewSigner(t)
	validArchive := updTarGz(t,
		updTarEntry{Name: "jelly-cms", Body: []byte("new executable is never run")},
		updTarEntry{Name: "web/index.html", Body: []byte("new web tree")},
	)
	for _, tc := range []struct {
		name   string
		assets map[string][]byte
	}{
		{name: "checksum rejected", assets: updAssets(signer, validArchive, strings.Repeat("0", 64), "")},
		{name: "signature rejected", assets: updAssets(signer, validArchive, "", "untrusted comment: forged\nAAAA\n")},
		{name: "archive rejected", assets: updAssets(signer, updTarGz(t, updTarEntry{Name: "unexpected", Body: []byte("bad")}), "", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, webIndex := updatePrebackupFixture(t, tc.assets, signer)
			calls := 0
			ctx := update.WithBeforeSwap(context.Background(), func(context.Context) error {
				calls++
				return nil
			})
			if err := update.Apply(ctx, "1.2.0"); err == nil {
				t.Fatal("Apply() accepted an invalid release")
			}
			if calls != 0 {
				t.Fatalf("before-swap callback ran %d times for an invalid release, want 0", calls)
			}
			if got := updReadFile(t, exe); got != "old executable" {
				t.Errorf("invalid release replaced executable: %q", got)
			}
			if got := updReadFile(t, webIndex); got != "old web tree" {
				t.Errorf("invalid release replaced web tree: %q", got)
			}
		})
	}
}

func TestUpdatePrebackupRegressionCallbackFailureAndCancellationPreventSwap(t *testing.T) {
	for _, tc := range []struct {
		name string
	}{
		{name: "callback error"},
		{name: "callback cancellation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer := updNewSigner(t)
			archive := updTarGz(t,
				updTarEntry{Name: "jelly-cms", Body: []byte("new executable is never run")},
				updTarEntry{Name: "web/index.html", Body: []byte("new web tree")},
			)
			exe, webIndex := updatePrebackupFixture(t, updAssets(signer, archive, "", ""), signer)
			calls := 0
			callbackErr := errors.New("pre-backup failed")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callback := func(gotCtx context.Context) error {
				calls++
				if gotCtx.Err() != nil {
					t.Errorf("callback context already cancelled: %v", gotCtx.Err())
				}
				if got := updReadFile(t, exe); got != "old executable" {
					t.Errorf("callback observed executable %q, want old executable", got)
				}
				if got := updReadFile(t, webIndex); got != "old web tree" {
					t.Errorf("callback observed web tree %q, want old web tree", got)
				}
				if tc.name == "callback cancellation" {
					cancel()
					return nil
				}
				return callbackErr
			}
			applyCtx := update.WithBeforeSwap(ctx, callback)
			err := update.Apply(applyCtx, "1.2.0")
			if err == nil {
				t.Fatal("Apply() succeeded after pre-update backup did not complete")
			}
			if tc.name == "callback error" && !errors.Is(err, callbackErr) {
				t.Errorf("Apply() error = %v, want callback error", err)
			}
			if tc.name == "callback cancellation" && !errors.Is(err, context.Canceled) {
				t.Errorf("Apply() error = %v, want context cancellation", err)
			}
			if calls != 1 {
				t.Errorf("before-swap callback ran %d times, want exactly once", calls)
			}
			if got := updReadFile(t, exe); got != "old executable" {
				t.Errorf("failed pre-update backup replaced executable: %q", got)
			}
			if got := updReadFile(t, webIndex); got != "old web tree" {
				t.Errorf("failed pre-update backup replaced web tree: %q", got)
			}
			state, stateErr := update.LoadState("data/update-state.json")
			if stateErr != nil {
				t.Fatalf("load update state: %v", stateErr)
			}
			if state.Status != "failed" {
				t.Errorf("update state = %q, want failed (healthcheck must not run)", state.Status)
			}
		})
	}
}

func TestUpdatePrebackupRegressionSystemApplyHandlerSnapshotsBeforeSwap(t *testing.T) {
	t.Run("success has verified job snapshot", func(t *testing.T) {
		h := newAccessHarness(t)
		admin := h.user("admin", "upd-system-prebackup-admin")
		signer := updNewSigner(t)
		newExecutable := []byte("#!/bin/sh\nprintf '%s\\n' '{\"status\":\"ok\",\"version\":\"v1.2.0\"}'\n")
		archive := updTarGz(t,
			updTarEntry{Name: "jelly-cms", Body: newExecutable},
			updTarEntry{Name: "web/index.html", Body: []byte("updated web tree")},
		)
		exe, webIndex := updatePrebackupFixture(t, updAssets(signer, archive, "", ""), signer)
		if err := config.Save(config.GlobalConfig); err != nil {
			t.Fatalf("save isolated update database config: %v", err)
		}
		jobID := uuid.NewString()
		payload, err := json.Marshal(map[string]any{"version": "1.2.0", "expected_version": "1.0.0", "actor": admin.ID})
		if err != nil {
			t.Fatal(err)
		}
		handler := updatePrebackupSystemHandler(t)
		job := queue.Job{ID: jobID, Kind: "update.apply", Payload: payload}
		if err := handler(context.Background(), h.db, job); err != nil {
			t.Fatalf("system update.apply handler: %v", err)
		}
		if got := updReadFile(t, exe); got != string(newExecutable) {
			t.Errorf("installed executable = %q, want fake healthcheck script", got)
		}
		if got := updReadFile(t, webIndex); got != "updated web tree" {
			t.Errorf("installed web tree = %q, want updated tree", got)
		}

		var row struct {
			ID        int64
			State     string
			Kind      string
			CreatedBy *uint
			Manifest  []byte
			FilePath  *string
		}
		if err := h.db.Raw(`SELECT id, state, kind, created_by, manifest, file_path FROM backups
			WHERE manifest->'_jelly_update'->>'job_id' = ?`, jobID).Take(&row).Error; err != nil {
			t.Fatalf("load update pre-backup row: %v", err)
		}
		if row.State != "done" || row.Kind != backup.KindManual || row.CreatedBy == nil || *row.CreatedBy != admin.ID {
			t.Errorf("update backup state/kind/actor = %q/%q/%v, want done/manual/%d", row.State, row.Kind, row.CreatedBy, admin.ID)
		}
		var manifest struct {
			Update struct {
				JobID  string `json:"job_id"`
				Target string `json:"target"`
			} `json:"_jelly_update"`
		}
		if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
			t.Fatalf("decode update backup manifest: %v", err)
		}
		if manifest.Update.JobID != jobID || manifest.Update.Target != "1.2.0" {
			t.Errorf("_jelly_update metadata = %+v, want job_id=%q target=1.2.0", manifest.Update, jobID)
		}
		if row.FilePath == nil {
			t.Fatal("update backup has no local archive path")
		}
		if _, err := os.Stat(*row.FilePath); err != nil {
			t.Fatalf("update backup archive does not exist: %v", err)
		}
		verified, err := backup.Verify(context.Background(), row.ID, "")
		if err != nil {
			t.Fatalf("Verify(update pre-backup): %v", err)
		}
		if !reflect.DeepEqual(verified.Contents, []string{backup.ContentDatabase}) {
			t.Errorf("update backup contents = %v, want database only", verified.Contents)
		}
	})

	t.Run("snapshot failure prevents swap", func(t *testing.T) {
		h := newAccessHarness(t)
		admin := h.user("admin", "upd-system-prebackup-fail-admin")
		signer := updNewSigner(t)
		archive := updTarGz(t,
			updTarEntry{Name: "jelly-cms", Body: []byte("#!/bin/sh\nexit 0\n")},
			updTarEntry{Name: "web/index.html", Body: []byte("updated web tree")},
		)
		exe, webIndex := updatePrebackupFixture(t, updAssets(signer, archive, "", ""), signer)
		if err := config.Save(config.GlobalConfig); err != nil {
			t.Fatalf("save isolated update database config: %v", err)
		}
		if err := h.db.Exec(`CREATE FUNCTION jelly_test_reject_update_backup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected update backup failure'; END; $$`).Error; err != nil {
			t.Fatalf("create backup failure function: %v", err)
		}
		if err := h.db.Exec(`CREATE TRIGGER jelly_test_reject_update_backup BEFORE INSERT ON backups FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_update_backup()`).Error; err != nil {
			t.Fatalf("create backup failure trigger: %v", err)
		}
		t.Cleanup(func() {
			_ = h.db.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_update_backup ON backups`).Error
			_ = h.db.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_update_backup()`).Error
		})
		jobID := uuid.NewString()
		payload, err := json.Marshal(map[string]any{"version": "1.2.0", "expected_version": "1.0.0", "actor": admin.ID})
		if err != nil {
			t.Fatal(err)
		}
		err = updatePrebackupSystemHandler(t)(context.Background(), h.db, queue.Job{ID: jobID, Kind: "update.apply", Payload: payload})
		if err == nil {
			t.Fatal("system update.apply handler succeeded after the required snapshot insert failed")
		}
		if got := updReadFile(t, exe); got != "old executable" {
			t.Errorf("snapshot failure replaced executable with %q", got)
		}
		if got := updReadFile(t, webIndex); got != "old web tree" {
			t.Errorf("snapshot failure replaced web tree with %q", got)
		}
		var count int64
		if err := h.db.Table("backups").Where("manifest->'_jelly_update'->>'job_id' = ?", jobID).Count(&count).Error; err != nil {
			t.Fatalf("count failed update backup rows: %v", err)
		}
		if count != 0 {
			t.Errorf("failed snapshot left %d backup rows, want none", count)
		}
	})
}

func updatePrebackupSystemHandler(t *testing.T) func(context.Context, *gorm.DB, queue.Job) error {
	t.Helper()
	systemRuntimeState.RLock()
	handler := systemRuntimeState.handlers["update.apply"]
	systemRuntimeState.RUnlock()
	if handler == nil {
		t.Fatal("update.apply system job handler is not registered")
	}
	return handler
}
