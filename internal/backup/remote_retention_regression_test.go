package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
)

func retentionRegressionRow(t *testing.T, kind string, created time.Time, marker string) record {
	t.Helper()
	if err := os.MkdirAll(Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Directory, marker+".bak")
	content := []byte(marker)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	row := record{
		State:      stateDone,
		Kind:       kind,
		Contents:   []byte(`["db"]`),
		FilePath:   &path,
		SizeBytes:  int64(len(content)),
		SHA256:     ptr(hex.EncodeToString(sum[:])),
		Manifest:   []byte(`{}`),
		CreatedAt:  created.UTC(),
		FinishedAt: ptr(created.UTC()),
	}
	if err := data.DB.Create(&row).Error; err != nil {
		t.Fatalf("insert %s backup fixture: %v", marker, err)
	}
	return row
}

func ptr[T any](v T) *T { return &v }

func setRetentionOptions(t *testing.T, values map[string]string) {
	t.Helper()
	if err := settings.Set(context.Background(), 0, values); err != nil {
		t.Fatalf("set retention options: %v", err)
	}
}

func assertRetentionRow(t *testing.T, row record, want bool) {
	t.Helper()
	var count int64
	if err := data.DB.Model(&record{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
		t.Fatalf("read retention row %d: %v", row.ID, err)
	}
	if got := count == 1; got != want {
		t.Errorf("backup %d (%s) retained = %t, want %t", row.ID, row.Kind, got, want)
	}
	if row.FilePath == nil {
		return
	}
	_, err := os.Stat(*row.FilePath)
	if want && err != nil {
		t.Errorf("retained archive %q is unavailable: %v", *row.FilePath, err)
	}
	if !want && !os.IsNotExist(err) {
		t.Errorf("pruned archive %q still exists or stat failed: %v", *row.FilePath, err)
	}
}

func TestBackupRetentionRegressionUsesSiteTimezoneAndProtectsManualBackups(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("save isolated retention config: %v", err)
	}
	setRetentionOptions(t, map[string]string{
		"site_timezone":     "America/Los_Angeles",
		"backup_keep_daily": "2",
	})

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// These two UTC dates are the same site-local backup date.
	olderSameLocalDay := retentionRegressionRow(t, KindScheduled, time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC), "same-local-day-old")
	newestSameLocalDay := retentionRegressionRow(t, KindScheduled, time.Date(2026, 10, 26, 6, 30, 0, 0, time.UTC), "same-local-day-new")
	previousLocalDay := retentionRegressionRow(t, KindScheduled, time.Date(2026, 10, 25, 5, 30, 0, 0, time.UTC), "previous-local-day")
	manual := retentionRegressionRow(t, KindManual, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), "manual-protected")
	preRestore := retentionRegressionRow(t, KindPreRestore, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), "pre-restore-protected")
	if gotA, gotB := olderSameLocalDay.CreatedAt.In(loc).Format("2006-01-02"), newestSameLocalDay.CreatedAt.In(loc).Format("2006-01-02"); gotA != gotB {
		t.Fatalf("fixture dates differ in site timezone: %s and %s", gotA, gotB)
	}

	if err := Prune(context.Background()); err != nil {
		t.Fatalf("Prune(): %v", err)
	}
	assertRetentionRow(t, newestSameLocalDay, true)
	assertRetentionRow(t, olderSameLocalDay, false)
	assertRetentionRow(t, previousLocalDay, true)
	assertRetentionRow(t, manual, true)
	assertRetentionRow(t, preRestore, true)
}

func TestBackupRetentionRegressionKeepsNewestPerISOWeekWithinCalendarWindow(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("save isolated retention config: %v", err)
	}
	setRetentionOptions(t, map[string]string{
		"site_timezone":             "UTC",
		"backup_keep_daily":         "1",
		"backup_keep_weekly_months": "3",
	})

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)
	// Seed the newest backup in each week across the calendar cutoff, plus a
	// duplicate in the current week. The daily and weekly sets are a union.
	var rows []record
	for week := 0; week <= 20; week++ {
		created := now.AddDate(0, 0, -7*week)
		rows = append(rows, retentionRegressionRow(t, KindScheduled, created, fmt.Sprintf("weekly-%02d", week)))
	}
	currentWeekDuplicate := retentionRegressionRow(t, KindScheduled, now.Add(-time.Hour), "current-week-older")
	if err := Prune(context.Background()); err != nil {
		t.Fatalf("Prune(): %v", err)
	}
	assertRetentionRow(t, currentWeekDuplicate, false)
	for _, row := range rows {
		localDate := row.CreatedAt.UTC().Truncate(24 * time.Hour)
		want := !localDate.Before(monthStart) || row.ID == rows[0].ID
		assertRetentionRow(t, row, want)
	}
}

type backupRemoteRegressionDriver struct {
	objects          map[string][]byte
	putErr           error
	openErr          error
	deleteErr        error
	corruptReadbacks bool
	putKeys          []string
	putSnapshots     [][]byte
	deleteCalls      int
}

func (d *backupRemoteRegressionDriver) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.putKeys = append(d.putKeys, key)
	d.putSnapshots = append(d.putSnapshots, append([]byte(nil), content...))
	d.objects[key] = append([]byte(nil), content...)
	return d.putErr
}

func (d *backupRemoteRegressionDriver) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.openErr != nil {
		return nil, d.openErr
	}
	content, ok := d.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	content = append([]byte(nil), content...)
	if d.corruptReadbacks && len(content) > 0 {
		content[0] ^= 1
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (d *backupRemoteRegressionDriver) Stat(_ context.Context, key string) (storage.Info, error) {
	content, ok := d.objects[key]
	if !ok {
		return storage.Info{}, os.ErrNotExist
	}
	return storage.Info{Path: key, Size: int64(len(content)), ModTime: time.Now()}, nil
}

func (d *backupRemoteRegressionDriver) Delete(_ context.Context, key string) error {
	d.deleteCalls++
	if d.deleteErr != nil {
		return d.deleteErr
	}
	if _, ok := d.objects[key]; !ok {
		return os.ErrNotExist
	}
	delete(d.objects, key)
	return nil
}

func (*backupRemoteRegressionDriver) URL(string) string          { return "" }
func (*backupRemoteRegressionDriver) Test(context.Context) error { return nil }

func backupRemoteRegressionSetup(t *testing.T, policyCount int) ([]int64, []*backupRemoteRegressionDriver) {
	t.Helper()
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("save isolated remote backup config: %v", err)
	}
	policyIDs := make([]int64, 0, policyCount)
	drivers := make([]*backupRemoteRegressionDriver, 0, policyCount)
	resolved := make(map[int64]*backupRemoteRegressionDriver, policyCount)
	for i := 0; i < policyCount; i++ {
		var policyID int64
		name := fmt.Sprintf("backup-remote-regression-%d", i)
		if err := data.DB.Raw(`INSERT INTO media_policies (name, driver, purpose, config)
			VALUES (?, 'local', 'backup', '{}'::jsonb) RETURNING id`, name).Scan(&policyID).Error; err != nil || policyID <= 0 {
			t.Fatalf("create isolated backup policy: id=%d err=%v", policyID, err)
		}
		driver := &backupRemoteRegressionDriver{objects: make(map[string][]byte)}
		policyIDs = append(policyIDs, policyID)
		drivers = append(drivers, driver)
		resolved[policyID] = driver
	}
	ConfigureStorageResolver(func(_ context.Context, id int64) (storage.Driver, error) {
		driver := resolved[id]
		if driver == nil {
			return nil, fmt.Errorf("no fake storage for policy %d", id)
		}
		return driver, nil
	})
	t.Cleanup(func() { ConfigureStorageResolver(nil) })
	if policyCount > 0 {
		setRetentionOptions(t, map[string]string{"backup_policy_id": fmt.Sprint(policyIDs[0])})
	}
	return policyIDs, drivers
}

func TestBackupRemoteRegressionUploadsAndVerifiesBeforeDone(t *testing.T) {
	policyIDs, drivers := backupRemoteRegressionSetup(t, 1)
	ctx := context.Background()
	id, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil || id <= 0 {
		t.Fatalf("Run() = %d, %v", id, err)
	}
	row, err := loadRecord(ctx, id)
	if err != nil {
		t.Fatalf("read completed remote backup: %v", err)
	}
	if row.State != stateDone || row.FilePath == nil || row.SHA256 == nil {
		t.Fatalf("remote backup row = state %q, file %v, checksum %v; want completed and locally staged", row.State, row.FilePath, row.SHA256)
	}
	metadata, ok := verifiedRemoteMetadata(row)
	if !ok || metadata.PolicyID != policyIDs[0] {
		t.Fatalf("verified remote metadata = %+v, valid %t; want policy %d", metadata, ok, policyIDs[0])
	}
	if len(drivers[0].putKeys) != 1 || drivers[0].putKeys[0] != metadata.Key {
		t.Fatalf("uploaded keys %v, remote metadata key %q", drivers[0].putKeys, metadata.Key)
	}
	localBytes, err := os.ReadFile(*row.FilePath)
	if err != nil {
		t.Fatalf("read staged local archive: %v", err)
	}
	remoteBytes, ok := drivers[0].objects[metadata.Key]
	if !ok || !bytes.Equal(remoteBytes, localBytes) {
		t.Fatalf("remote archive bytes differ from encrypted local archive (exists=%t)", ok)
	}
	if metadata.Size != int64(len(localBytes)) || metadata.SHA256 == "" {
		t.Errorf("remote metadata size/checksum = %d/%q for local bytes %d", metadata.Size, metadata.SHA256, len(localBytes))
	}
	sum := sha256.Sum256(localBytes)
	if metadata.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("remote metadata checksum = %q, want %x", metadata.SHA256, sum)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
		t.Fatalf("decode database manifest: %v", err)
	}
	if len(manifest["_jelly_remote"]) == 0 || len(manifest["_jelly_pending"]) != 0 {
		t.Errorf("finished database manifest markers = %s, want _jelly_remote only", row.Manifest)
	}
	info, err := os.Stat(*row.FilePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("staged archive mode = %v, stat error %v; want 0600", info, err)
	}
}

func TestBackupRemoteRegressionReadbackFailureRetriesExactEncryptedArchive(t *testing.T) {
	_, drivers := backupRemoteRegressionSetup(t, 1)
	store := drivers[0]
	store.corruptReadbacks = true
	ctx := context.Background()
	id, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err == nil || id <= 0 {
		t.Fatalf("Run() after corrupted remote readback = %d, %v; want failed row and verification error", id, err)
	}
	failed, err := loadRecord(ctx, id)
	if err != nil || failed.State != stateFailed || failed.FilePath == nil {
		t.Fatalf("failed remote backup row = %+v, read error %v; want failed with original local archive", failed, err)
	}
	original, err := os.ReadFile(*failed.FilePath)
	if err != nil {
		t.Fatalf("read preserved original encrypted archive: %v", err)
	}
	if len(store.putSnapshots) != 1 || !bytes.Equal(store.putSnapshots[0], original) {
		t.Fatalf("first upload bytes do not match preserved archive")
	}
	failedMeta := decodeDatabaseManifest(failed.Manifest).pending
	if failedMeta == nil || failedMeta.TransferKey == "" || failedMeta.TransferSHA == "" || failedMeta.TransferSize != int64(len(original)) {
		t.Fatalf("failed transfer metadata = %+v; want saved object key/checksum/size", failedMeta)
	}

	store.corruptReadbacks = false
	retryID, err := Retry(ctx, id, "", 0)
	if err != nil || retryID != id {
		t.Fatalf("Retry() = %d, %v; want same backup ID %d", retryID, err, id)
	}
	if len(store.putSnapshots) != 2 || !bytes.Equal(store.putSnapshots[1], original) {
		t.Fatalf("retry did not re-upload the exact original encrypted archive")
	}
	if store.putKeys[0] != store.putKeys[1] || store.putKeys[0] != failedMeta.TransferKey {
		t.Errorf("retry object keys = %v, want original transfer key %q", store.putKeys, failedMeta.TransferKey)
	}
	completed, err := loadRecord(ctx, id)
	if err != nil || completed.State != stateDone {
		t.Fatalf("retried row = state %q err %v; want done", completed.State, err)
	}
	if meta, ok := verifiedRemoteMetadata(completed); !ok || meta.Key != failedMeta.TransferKey {
		t.Errorf("completed remote metadata = %+v valid=%t; want same transfer key", meta, ok)
	}
}

func TestBackupRemoteRegressionQueuedPolicySnapshotAndRemoteRestore(t *testing.T) {
	policyIDs, drivers := backupRemoteRegressionSetup(t, 2)
	ctx := context.Background()
	id, err := PrepareQueued(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil || id <= 0 {
		t.Fatalf("PrepareQueued() = %d, %v", id, err)
	}
	prepared, err := loadRecord(ctx, id)
	if err != nil {
		t.Fatalf("read prepared backup: %v", err)
	}
	if pending := decodeDatabaseManifest(prepared.Manifest).pending; pending == nil || pending.PolicyID != policyIDs[0] {
		t.Fatalf("queued destination metadata = %+v; want fixed policy %d", pending, policyIDs[0])
	}
	if err := settings.Set(ctx, 0, map[string]string{"backup_policy_id": fmt.Sprint(policyIDs[1])}); err != nil {
		t.Fatalf("change configured destination after enqueue: %v", err)
	}
	if gotID, err := RunPrepared(ctx, id); err != nil || gotID != id {
		t.Fatalf("RunPrepared() = %d, %v; want %d", gotID, err, id)
	}
	completed, err := loadRecord(ctx, id)
	if err != nil {
		t.Fatalf("read completed queued backup: %v", err)
	}
	if meta, ok := verifiedRemoteMetadata(completed); !ok || meta.PolicyID != policyIDs[0] {
		t.Fatalf("completed queued metadata = %+v valid=%t; want policy %d", meta, ok, policyIDs[0])
	}
	if len(drivers[0].objects) != 1 || len(drivers[1].objects) != 0 {
		t.Fatalf("object counts after queued execution = [%d %d], want upload only to policy snapshot", len(drivers[0].objects), len(drivers[1].objects))
	}
	if completed.FilePath == nil {
		t.Fatal("completed queued backup has no local path")
	}
	if err := os.Remove(*completed.FilePath); err != nil {
		t.Fatalf("remove local archive to force verified remote read: %v", err)
	}
	archivePath, downloadName, err := ArchivePath(ctx, id)
	if err != nil || downloadName != fmt.Sprintf("jelly-backup-%d.jbk", id) {
		t.Fatalf("ArchivePath() = %q, %q, %v", archivePath, downloadName, err)
	}
	info, err := os.Stat(archivePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("download cache mode = %v, stat error %v; want 0600", info, err)
	}
	cacheInfo, err := os.Stat(filepath.Dir(archivePath))
	if err != nil || cacheInfo.Mode().Perm() != 0o700 {
		t.Errorf("download cache directory mode = %v, stat error %v; want 0700", cacheInfo, err)
	}
	verified, err := Verify(ctx, id, "")
	if err != nil || verified.Version != manifestVersion {
		t.Fatalf("Verify() from remote = version %d, %v", verified.Version, err)
	}
	if err := settings.Set(ctx, 0, map[string]string{"backup_policy_id": "0"}); err != nil {
		t.Fatalf("disable remote destination for local pre-restore snapshot: %v", err)
	}
	result, err := Restore(ctx, RestoreRequest{ID: id, Confirm: RestoreConfirmation})
	if err != nil || result.PreRestoreID <= 0 {
		t.Fatalf("Restore() from verified remote archive = %+v, %v", result, err)
	}
}

func TestBackupRemoteRegressionDeleteFailuresKeepRetryableReference(t *testing.T) {
	_, drivers := backupRemoteRegressionSetup(t, 1)
	store := drivers[0]
	ctx := context.Background()
	id, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil {
		t.Fatalf("create remote backup: %v", err)
	}
	row, err := loadRecord(ctx, id)
	if err != nil || row.FilePath == nil {
		t.Fatalf("read remote backup row: %+v, %v", row, err)
	}
	meta, ok := verifiedRemoteMetadata(row)
	if !ok {
		t.Fatalf("backup has no verified remote metadata: %+v", row)
	}
	store.deleteErr = errors.New("injected remote delete failure")
	if err := Delete(ctx, id); err == nil {
		t.Fatal("Delete() succeeded despite remote object deletion failure")
	}
	if _, err := loadRecord(ctx, id); err != nil {
		t.Errorf("remote delete failure removed the database reference: %v", err)
	}
	if _, err := os.Stat(*row.FilePath); err != nil {
		t.Errorf("remote delete failure removed the local archive: %v", err)
	}
	store.deleteErr = nil
	if err := data.DB.Exec(`CREATE FUNCTION jelly_test_reject_backup_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected row delete failure'; END; $$`).Error; err != nil {
		t.Fatalf("create backup delete failure function: %v", err)
	}
	if err := data.DB.Exec(`CREATE TRIGGER jelly_test_reject_backup_delete BEFORE DELETE ON backups FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_backup_delete()`).Error; err != nil {
		t.Fatalf("create backup delete failure trigger: %v", err)
	}
	if err := Delete(ctx, id); err == nil {
		t.Fatal("Delete() succeeded despite injected database row deletion failure")
	}
	if _, err := loadRecord(ctx, id); err != nil {
		t.Errorf("database delete failure removed backup reference: %v", err)
	}
	if _, ok := store.objects[meta.Key]; ok {
		t.Errorf("remote object still exists after successful remote-delete attempt")
	}
	if err := data.DB.Exec(`DROP TRIGGER jelly_test_reject_backup_delete ON backups`).Error; err != nil {
		t.Fatalf("drop injected backup delete trigger: %v", err)
	}
	if err := data.DB.Exec(`DROP FUNCTION jelly_test_reject_backup_delete()`).Error; err != nil {
		t.Fatalf("drop injected backup delete function: %v", err)
	}
	if err := Delete(ctx, id); err != nil {
		t.Fatalf("retry Delete() after prior remote success and row failure: %v", err)
	}
	if _, err := loadRecord(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("backup row after successful delete retry = %v, want not found", err)
	}
}

func TestBackupRetentionRegressionLocalKeepOnlyPrunesVerifiedRemoteCopies(t *testing.T) {
	policyIDs, drivers := backupRemoteRegressionSetup(t, 1)
	ctx := context.Background()
	remoteID, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil {
		t.Fatalf("create scheduled remote backup: %v", err)
	}
	remoteRow, err := loadRecord(ctx, remoteID)
	if err != nil || remoteRow.FilePath == nil {
		t.Fatalf("read scheduled remote backup: %+v, %v", remoteRow, err)
	}
	remoteMeta, ok := verifiedRemoteMetadata(remoteRow)
	if !ok || remoteMeta.PolicyID != policyIDs[0] {
		t.Fatalf("scheduled remote metadata = %+v valid=%t", remoteMeta, ok)
	}
	if err := settings.Set(ctx, 0, map[string]string{
		"backup_policy_id":          "0",
		"backup_keep_daily":         "10",
		"backup_keep_weekly_months": "0",
		"backup_local_keep":         "0",
	}); err != nil {
		t.Fatalf("select local-only mode and local retention: %v", err)
	}
	localID, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil {
		t.Fatalf("create scheduled local-only backup: %v", err)
	}
	localRow, err := loadRecord(ctx, localID)
	if err != nil || localRow.FilePath == nil {
		t.Fatalf("read local-only backup: %+v, %v", localRow, err)
	}
	// Separate the fixtures into distinct site-local backup dates so both rows
	// survive daily/weekly retention and only backup_local_keep is under test.
	if err := data.DB.Model(&record{}).Where("id = ?", remoteID).Update("created_at", time.Now().Add(-24*time.Hour)).Error; err != nil {
		t.Fatalf("place remote fixture on previous day: %v", err)
	}
	if err := data.DB.Model(&record{}).Where("id = ?", localID).Update("created_at", time.Now()).Error; err != nil {
		t.Fatalf("place local fixture on current day: %v", err)
	}
	if err := Prune(ctx); err != nil {
		t.Fatalf("Prune() with local_keep=0: %v", err)
	}
	if _, err := loadRecord(ctx, remoteID); err != nil {
		t.Errorf("remote backup row was removed when only its local copy should be pruned: %v", err)
	}
	if _, err := os.Stat(*remoteRow.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("verified remote local copy still exists or stat failed: %v", err)
	}
	if _, ok := drivers[0].objects[remoteMeta.Key]; !ok {
		t.Errorf("Prune removed verified remote object %q", remoteMeta.Key)
	}
	if _, err := loadRecord(ctx, localID); err != nil {
		t.Errorf("local-only backup row was pruned with local_keep=0: %v", err)
	}
	if _, err := os.Stat(*localRow.FilePath); err != nil {
		t.Errorf("local-only archive was pruned with local_keep=0: %v", err)
	}
}

func TestBackupRemoteRegressionRejectsCancelledRemoteReadWithoutCache(t *testing.T) {
	_, drivers := backupRemoteRegressionSetup(t, 1)
	ctx := context.Background()
	id, err := Run(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindScheduled})
	if err != nil {
		t.Fatalf("create remote backup: %v", err)
	}
	row, err := loadRecord(ctx, id)
	if err != nil || row.FilePath == nil {
		t.Fatalf("read remote backup: %+v, %v", row, err)
	}
	if err := os.Remove(*row.FilePath); err != nil {
		t.Fatalf("remove local archive: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := ArchivePath(cancelled, id); !errors.Is(err, context.Canceled) {
		t.Errorf("ArchivePath() with cancelled remote read error = %v, want context cancellation", err)
	}
	cacheDir := filepath.Join(Directory, ".private-cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect remote cache after cancelled read: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cancelled remote read left cache entries: %v", entries)
	}
	if len(drivers[0].objects) != 1 {
		t.Errorf("cancelled archive read changed remote object count: %d", len(drivers[0].objects))
	}
}

var _ storage.Driver = (*backupRemoteRegressionDriver)(nil)
