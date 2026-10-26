package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"github.com/google/uuid"
)

func setupPreUpdateBackupRegression(t *testing.T) (context.Context, uint, string, string, string) {
	t.Helper()
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("save isolated pre-update config: %v", err)
	}
	if err := os.MkdirAll("data", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := auth.Init(); err != nil {
		t.Fatalf("initialize isolated auth secrets: %v", err)
	}
	actorUser := core.User{Username: "pre-update-regression-actor", Email: "pre-update-regression-actor@example.test", Role: "admin"}
	if err := data.DB.Create(&actorUser).Error; err != nil {
		t.Fatalf("create isolated update actor: %v", err)
	}
	var policyID int64
	if err := data.DB.Raw(`INSERT INTO media_policies (name, driver, purpose, config)
		VALUES (?, 's3', 'backup', '{}'::jsonb) RETURNING id`, "pre-update-s3-fixture").Scan(&policyID).Error; err != nil || policyID <= 0 {
		t.Fatalf("create fake S3 backup policy: id=%d err=%v", policyID, err)
	}
	ctx := context.Background()
	if err := settings.Set(ctx, 0, map[string]string{"backup_policy_id": fmt.Sprint(policyID)}); err != nil {
		t.Fatalf("select fake S3 backup policy: %v", err)
	}
	if err := data.DB.Create(&core.Option{Key: "pre-update-backup-regression", Value: "snapshot-present"}).Error; err != nil {
		t.Fatalf("seed isolated database snapshot marker: %v", err)
	}
	resolverCalls := 0
	ConfigureStorageResolver(func(context.Context, int64) (storage.Driver, error) {
		resolverCalls++
		return nil, errors.New("intentional S3 resolver failure")
	})
	t.Cleanup(func() { ConfigureStorageResolver(nil) })
	t.Cleanup(func() {
		if resolverCalls != 0 {
			t.Errorf("pre-update local backup attempted remote S3 resolution %d times", resolverCalls)
		}
	})
	return ctx, actorUser.ID, uuid.NewString(), "1.2.0", actorUser.Username
}

func TestBackupPreUpdateRegressionCreatesLocalVerifiedBackupAndReplays(t *testing.T) {
	ctx, actor, jobID, target, actorUsername := setupPreUpdateBackupRegression(t)
	id, err := RunBeforeUpdate(ctx, actor, jobID, target)
	if err != nil || id <= 0 {
		t.Fatalf("RunBeforeUpdate() = %d, %v", id, err)
	}
	row, err := loadRecord(ctx, id)
	if err != nil {
		t.Fatalf("load pre-update backup row: %v", err)
	}
	if row.State != stateDone {
		t.Fatalf("pre-update backup state = %q, want done", row.State)
	}
	if row.Kind != KindManual {
		t.Errorf("pre-update backup kind = %q, want %q", row.Kind, KindManual)
	}
	if row.CreatedBy == nil || *row.CreatedBy != actor {
		t.Errorf("pre-update backup actor = %v, want %d", row.CreatedBy, actor)
	}
	var outerManifest map[string]json.RawMessage
	if err := json.Unmarshal(row.Manifest, &outerManifest); err != nil {
		t.Fatalf("decode database backup manifest: %v", err)
	}
	var updateMetadata struct {
		JobID  string `json:"job_id"`
		Target string `json:"target"`
	}
	if raw := outerManifest["_jelly_update"]; len(raw) == 0 {
		t.Fatal("pre-update backup manifest is missing _jelly_update metadata")
	} else if err := json.Unmarshal(raw, &updateMetadata); err != nil {
		t.Fatalf("decode _jelly_update metadata: %v", err)
	}
	if updateMetadata.JobID != jobID || updateMetadata.Target != target {
		t.Errorf("_jelly_update metadata = %+v, want job_id=%q target=%q", updateMetadata, jobID, target)
	}
	verified, err := Verify(ctx, id, "")
	if err != nil {
		t.Fatalf("verify local pre-update archive: %v", err)
	}
	if !reflect.DeepEqual(verified.Contents, []string{ContentDatabase}) {
		t.Fatalf("pre-update archive contents = %v, want only the database snapshot", verified.Contents)
	}
	if row.FilePath == nil || row.SHA256 == nil {
		t.Fatal("completed pre-update backup has no local archive path or checksum")
	}
	stage := t.TempDir()
	if _, err := stageArchive(*row.FilePath, *row.SHA256, "", stage); err != nil {
		t.Fatalf("decrypt and extract pre-update archive: %v", err)
	}
	usersJSONL, err := os.ReadFile(filepath.Join(stage, contentDir, dbPrefix+"users"+dbSuffix))
	if err != nil {
		t.Fatalf("read user rows from pre-update archive: %v", err)
	}
	if !strings.Contains(string(usersJSONL), `"username":"`+actorUsername+`"`) {
		t.Errorf("pre-update database snapshot does not contain actor %q: %s", actorUsername, usersJSONL)
	}

	replayID, err := RunBeforeUpdate(ctx, actor, jobID, target)
	if err != nil || replayID != id {
		t.Fatalf("healthy pre-update replay = %d, %v; want existing ID %d", replayID, err, id)
	}
	var count int64
	if err := data.DB.Model(&record{}).Where("kind = ? AND manifest::text LIKE ?", KindManual, "%"+jobID+"%").Count(&count).Error; err != nil {
		t.Fatalf("count pre-update backup records: %v", err)
	}
	if count != 1 {
		t.Errorf("pre-update backup records for %q = %d, want one", jobID, count)
	}

	row, err = loadRecord(ctx, id)
	if err != nil || row.FilePath == nil {
		t.Fatalf("reload pre-update backup archive path: row=%+v err=%v", row, err)
	}
	if err := os.WriteFile(*row.FilePath, []byte("corrupt pre-update archive"), 0o600); err != nil {
		t.Fatalf("corrupt pre-update archive fixture: %v", err)
	}
	regeneratedID, replayErr := RunBeforeUpdate(ctx, actor, jobID, target)
	if replayErr != nil {
		return // refusing a damaged existing archive is a valid fail-closed result
	}
	if regeneratedID <= 0 {
		t.Fatalf("damaged archive replay returned success without an ID: %d", regeneratedID)
	}
	if _, err := Verify(ctx, regeneratedID, ""); err != nil {
		t.Fatalf("successful damaged-archive replay did not regenerate a valid archive: %v", err)
	}
}

func TestBackupPreUpdateRegressionCrossNodeBusyRetryUsesOneSnapshot(t *testing.T) {
	ctx, actor, jobID, target, _ := setupPreUpdateBackupRegression(t)
	sqlDB, err := data.DB.DB()
	if err != nil {
		t.Fatalf("get isolated SQL database: %v", err)
	}
	lockConn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("open independent node lock connection: %v", err)
	}
	classID, lockKey := preUpdateRegressionLockKey(jobID, target)
	var acquired bool
	if err := lockConn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, classID, lockKey).Scan(&acquired); err != nil {
		_ = lockConn.Close()
		t.Fatalf("acquire simulated other-node lock: %v", err)
	}
	if !acquired {
		_ = lockConn.Close()
		t.Fatal("failed to acquire simulated other-node advisory lock")
	}
	id, callErr := RunBeforeUpdate(ctx, actor, jobID, target)
	if id != 0 || !errors.Is(callErr, ErrBusy) {
		t.Errorf("RunBeforeUpdate with other-node lock = %d, %v; want 0, ErrBusy", id, callErr)
	}
	var count int64
	if err := data.DB.Model(&record{}).Where("kind = ? AND manifest->'_jelly_update'->>'job_id' = ?", KindManual, jobID).Count(&count).Error; err != nil {
		t.Fatalf("count rows while other node holds lock: %v", err)
	}
	if count != 0 {
		t.Fatalf("RunBeforeUpdate created %d rows while another node held the lock", count)
	}
	var released bool
	if err := lockConn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, classID, lockKey).Scan(&released); err != nil || !released {
		t.Fatalf("release simulated other-node lock: released=%v err=%v", released, err)
	}
	if err := lockConn.Close(); err != nil {
		t.Fatalf("close simulated other-node connection: %v", err)
	}

	firstID, err := RunBeforeUpdate(ctx, actor, jobID, target)
	if err != nil || firstID <= 0 {
		t.Fatalf("retry after other-node unlock = %d, %v", firstID, err)
	}
	replayID, err := RunBeforeUpdate(ctx, actor, jobID, target)
	if err != nil || replayID != firstID {
		t.Fatalf("healthy replay = %d, %v; want original ID %d", replayID, err, firstID)
	}
	if err := data.DB.Model(&record{}).Where("kind = ? AND manifest->'_jelly_update'->>'job_id' = ? AND manifest->'_jelly_update'->>'target' = ?", KindManual, jobID, target).Count(&count).Error; err != nil {
		t.Fatalf("count completed pre-update rows: %v", err)
	}
	if count != 1 {
		t.Errorf("completed rows after retry and replay = %d, want 1", count)
	}

	probe, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("open lock-release probe: %v", err)
	}
	defer probe.Close()
	if err := probe.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, classID, lockKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("successful pre-update run retained its session lock: acquired=%v err=%v", acquired, err)
	}
	if err := probe.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, classID, lockKey).Scan(&released); err != nil || !released {
		t.Fatalf("release lock probe: released=%v err=%v", released, err)
	}
}

func TestBackupPreUpdateRegressionCancellationReleasesSessionLock(t *testing.T) {
	ctx, actor, jobID, target, _ := setupPreUpdateBackupRegression(t)
	sqlDB, err := data.DB.DB()
	if err != nil {
		t.Fatalf("get isolated SQL database: %v", err)
	}
	blocker, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("open table-lock connection: %v", err)
	}
	tx, err := blocker.BeginTx(ctx, nil)
	if err != nil {
		_ = blocker.Close()
		t.Fatalf("begin table-lock transaction: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE backups IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = tx.Rollback()
		_ = blocker.Close()
		t.Fatalf("lock backup rows during cancellation test: %v", err)
	}
	classID, lockKey := preUpdateRegressionLockKey(jobID, target)
	applyCtx, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		_, err := RunBeforeUpdate(applyCtx, actor, jobID, target)
		finished <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var held bool
		if err := data.DB.Raw(`SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND classid = ?::oid AND objid = ?::oid AND objsubid = 2 AND granted)`, int64(uint32(classID)), int64(uint32(lockKey))).Scan(&held).Error; err != nil {
			cancel()
			_ = tx.Rollback()
			_ = blocker.Close()
			t.Fatalf("observe acquired pre-update session lock: %v", err)
		}
		if held {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = tx.Rollback()
			_ = blocker.Close()
			t.Fatal("RunBeforeUpdate did not acquire its advisory lock while waiting for backups table")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled RunBeforeUpdate = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		_ = tx.Rollback()
		_ = blocker.Close()
		t.Fatal("RunBeforeUpdate did not return after context cancellation")
	}
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Errorf("release backup table lock: %v", err)
	}
	if err := blocker.Close(); err != nil {
		t.Errorf("close table-lock connection: %v", err)
	}

	probe, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("open post-cancel lock probe: %v", err)
	}
	defer probe.Close()
	var acquired bool
	if err := probe.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, classID, lockKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("cancelled pre-update run retained its session lock: acquired=%v err=%v", acquired, err)
	}
	var released bool
	if err := probe.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1, $2)`, classID, lockKey).Scan(&released); err != nil || !released {
		t.Fatalf("release post-cancel lock probe: released=%v err=%v", released, err)
	}
	id, retryErr := RunBeforeUpdate(ctx, actor, jobID, target)
	if retryErr != nil || id <= 0 {
		t.Fatalf("retry after cancellation = %d, %v", id, retryErr)
	}
}

func preUpdateRegressionLockKey(jobID, target string) (int32, int32) {
	digest := sha256.Sum256([]byte(jobID + "\x00" + target))
	return 731984206, int32(binary.BigEndian.Uint32(digest[:4]))
}
