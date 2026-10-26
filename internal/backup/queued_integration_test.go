package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

func TestQueuedBackupPrepareRunAndReplay(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("save isolated config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := auth.Init(); err != nil {
		t.Fatalf("initialize isolated auth secrets: %v", err)
	}

	const passphrase = "Queued-Backup-Secret-123"
	id, err := PrepareQueued(context.Background(), Request{
		Contents:   []string{ContentDatabase},
		Note:       "queued integration regression",
		Passphrase: passphrase,
	})
	if err != nil || id <= 0 {
		t.Fatalf("PrepareQueued() = %d, %v", id, err)
	}
	var prepared record
	if err := data.DB.Take(&prepared, id).Error; err != nil {
		t.Fatalf("read prepared backup row: %v", err)
	}
	if prepared.State != stateQueued {
		t.Fatalf("prepared state = %q, want %q", prepared.State, stateQueued)
	}
	if strings.Contains(string(prepared.Manifest), passphrase) {
		t.Fatal("queued manifest contains passphrase in plaintext")
	}
	var metadata queuedManifest
	if err := json.Unmarshal(prepared.Manifest, &metadata); err != nil {
		t.Fatalf("decode queued manifest: %v", err)
	}
	if metadata.Pending == nil || len(metadata.Pending.PassphraseEnc) == 0 {
		t.Fatalf("queued manifest has no sealed passphrase: %+v", metadata)
	}

	runID, err := RunPrepared(context.Background(), id)
	if err != nil || runID != id {
		t.Fatalf("RunPrepared() = %d, %v; want prepared ID %d", runID, err, id)
	}
	var completed record
	if err := data.DB.Take(&completed, id).Error; err != nil {
		t.Fatalf("read completed backup row: %v", err)
	}
	if completed.State != "done" || completed.FilePath == nil || completed.FinishedAt == nil {
		t.Fatalf("prepared backup did not finish: %+v", completed)
	}
	archive, err := os.ReadFile(*completed.FilePath)
	if err != nil {
		t.Fatalf("read generated backup archive: %v", err)
	}
	if strings.Contains(string(archive), passphrase) {
		t.Fatal("backup archive contains passphrase in plaintext")
	}

	replayID, err := RunPrepared(context.Background(), id)
	if err != nil || replayID != id {
		t.Fatalf("idempotent RunPrepared() = %d, %v; want %d", replayID, err, id)
	}
	var count int64
	if err := data.DB.Model(&record{}).Where("id = ?", id).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("queued backup row count = %d, %v; want one", count, err)
	}
}
