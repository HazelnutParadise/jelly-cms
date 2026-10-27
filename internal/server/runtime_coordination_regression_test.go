package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"gorm.io/gorm"
)

func TestRuntimeCoordinationSystemJobEnqueuerIsScopedToDatabase(t *testing.T) {
	h1 := newAccessHarness(t)
	SetSystemDatabase(h1.db)
	stop := StartSystemServices(context.Background())
	t.Cleanup(func() {
		stop()
		SetSystemDatabase(nil)
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _, ready := systemQueueForDB(h1.db)
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("system services did not start for the first test database")
		}
		time.Sleep(10 * time.Millisecond)
	}

	job := queue.Job{
		Queue:     queue.QueueWebhook,
		Kind:      "runtime.database-scope.contract",
		DedupeKey: "runtime-db-scope-contract",
		Payload:   json.RawMessage(`{"origin":1}`),
	}
	delivered := make(chan *gorm.DB, 1)
	RegisterSystemJobHandler(job.Kind, func(_ context.Context, db *gorm.DB, _ queue.Job) error {
		select {
		case delivered <- db:
		default:
		}
		return nil
	})
	enqueueFirst := SystemJobEnqueuer(h1.db)
	id, err := enqueueFirst(context.Background(), job)
	if err != nil || id == "" {
		t.Fatalf("first database enqueue = %q, %v", id, err)
	}
	secondID, err := SystemJobEnqueuer(h1.db)(context.Background(), job)
	if err != nil || secondID != id {
		t.Fatalf("second echo/database-bound enqueuer = %q, %v; want same durable ID %q", secondID, err, id)
	}
	if n := impCount(t, h1, `SELECT COUNT(*) FROM jobs WHERE dedupe_key = ? AND id::text = ?`, job.DedupeKey, id); n != 1 {
		t.Fatalf("first database has %d matching durable jobs, want one", n)
	}

	// Open a second independent database without changing process-global site
	// settings. Its bound enqueuer must not submit to the first site's queue.
	admin, err := data.Open(config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: accessTestAdminDB, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated database admin connection: %v", err)
	}
	otherName := fmt.Sprintf("%s%d_%d", accessTestPrefix, os.Getpid(), accessTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, otherName)).Error; err != nil {
		accessCloseDB(admin)
		t.Fatalf("create second isolated site database: %v", err)
	}
	var otherDB *gorm.DB
	t.Cleanup(func() {
		if otherDB != nil {
			accessCloseDB(otherDB)
		}
		_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, otherName)).Error
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", otherName).Error
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, otherName)).Error; err != nil {
			t.Errorf("drop second isolated site database: %v", err)
		}
		accessCloseDB(admin)
	})
	otherDB, err = data.Open(config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: otherName, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open second isolated site database: %v", err)
	}
	if err := data.MigrateDB(otherDB); err != nil {
		t.Fatalf("migrate second isolated site database: %v", err)
	}
	if secondID, err := SystemJobEnqueuer(otherDB)(context.Background(), job); err == nil {
		t.Fatalf("second database enqueued through the first site's runtime as %q", secondID)
	}
	var otherRows int64
	if err := otherDB.Raw(`SELECT COUNT(*) FROM jobs WHERE dedupe_key = ?`, job.DedupeKey).Scan(&otherRows).Error; err != nil {
		t.Fatalf("query second site's queue rows: %v", err)
	}
	if otherRows != 0 {
		t.Errorf("second database unexpectedly contains %d matching jobs", otherRows)
	}
	if n := impCount(t, h1, `SELECT COUNT(*) FROM jobs WHERE dedupe_key = ? AND id::text = ?`, job.DedupeKey, id); n != 1 {
		t.Errorf("second database attempt changed the first site's queue; rows=%d", n)
	}
	if sameID, err := SystemJobEnqueuer(h1.db)(context.Background(), job); err != nil || sameID != id {
		t.Errorf("first database enqueuer after second DB setup = %q, %v; want %q", sameID, err, id)
	}
	select {
	case processedDB := <-delivered:
		if processedDB != h1.db {
			t.Errorf("handler received database %p, want first site database %p", processedDB, h1.db)
		}
	case <-time.After(5 * time.Second):
		t.Error("system job handler did not receive the database-scoped job")
	}
}

func TestRuntimeCoordinationRestoreBlocksPublicMediaUploadBeforeWrite(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "runtime-restore-gate-admin")
	token := h.token(admin)
	if err := setMaintenance(h.db, false, ""); err != nil {
		t.Fatalf("seed maintenance option rows: %v", err)
	}
	medPolicy(t, h, token, "Restore gate uploads")
	originalPayload := medPNG()
	original := medConsistencyUpload(h, token, "/api/admin/media/files", "restore-original.png", originalPayload)
	if original.code < 200 || original.code >= 300 {
		t.Fatalf("upload original attachment = %d: %s", original.code, original.body)
	}
	var originalPath string
	if err := h.db.Raw(`SELECT path FROM media_files WHERE filename = ? AND deleted_at IS NULL`, "restore-original.png").Scan(&originalPath).Error; err != nil || originalPath == "" {
		t.Fatalf("read original attachment path %q: %v", originalPath, err)
	}
	originalFile := filepath.Join("data", "uploads", filepath.FromSlash(originalPath))
	if got, err := os.ReadFile(originalFile); err != nil || string(got) != string(originalPayload) {
		t.Fatalf("original attachment bytes before backup = %q, err %v", got, err)
	}
	backupResult := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db","attachments"],"note":"restore gate fixture"}`, token, false)
	if backupResult.code != http.StatusAccepted {
		t.Fatalf("create real database and attachment backup = %d: %s", backupResult.code, backupResult.body)
	}
	backupRow := bkpLatest(t, h)
	if backupRow.State != "done" {
		t.Fatalf("test-mode backup state = %q, want done", backupRow.State)
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		t.Fatalf("get database pool: %v", err)
	}
	lockTx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin maintenance row-lock transaction: %v", err)
	}
	var maintenanceValue string
	if err := lockTx.QueryRowContext(context.Background(), `SELECT value FROM options WHERE key = 'maintenance_enabled' FOR UPDATE`).Scan(&maintenanceValue); err != nil {
		_ = lockTx.Rollback()
		t.Fatalf("lock maintenance option row: %v", err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lockTx.Rollback()
		}
	}()

	restoreDone := make(chan accessResult, 1)
	go func() {
		restoreDone <- bkpCall(h, http.MethodPost, fmt.Sprintf("/api/admin/backups/%d/restore", backupRow.ID), `{"confirm":"RESTORE"}`, token, false)
	}()
	if !waitForRuntimeCoordinationActivity(t, h.db, 8*time.Second, `state = 'active' AND wait_event_type = 'Lock' AND query ILIKE '%options%' AND query ILIKE '%FOR UPDATE%'`) {
		t.Fatal("restore never reached the maintenance option row lock while holding the exclusive system write gate")
	}

	newName := "restore-gated-upload.png"
	newPayload := append([]byte(nil), medPNG()...)
	uploadDone := make(chan accessResult, 1)
	go func() {
		uploadDone <- medConsistencyUpload(h, token, "/api/admin/media/files", newName, newPayload)
	}()
	if !waitForRuntimeCoordinationActivity(t, h.db, 8*time.Second, `state = 'active' AND wait_event_type = 'Lock' AND query ILIKE '%pg_advisory_lock_shared%'`) {
		t.Fatal("public media upload did not wait for the shared system write gate behind restore")
	}
	if _, err := os.Stat(filepath.Join("data", "uploads")); err != nil {
		t.Fatalf("inspect uploads directory before releasing restore: %v", err)
	}
	var newFileMatches []string
	err = filepath.WalkDir(filepath.Join("data", "uploads"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == newName {
			newFileMatches = append(newFileMatches, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk uploads tree before release: %v", err)
	}
	if len(newFileMatches) != 0 {
		t.Fatalf("public upload wrote physical file before restore completed: %v", newFileMatches)
	}
	var newRows int64
	if err := h.db.Model(&struct{ ID int64 }{}).Table("media_files").Where("filename = ? AND deleted_at IS NULL", newName).Count(&newRows).Error; err != nil {
		t.Fatalf("count gated upload rows before release: %v", err)
	}
	if newRows != 0 {
		t.Fatalf("public upload wrote %d media rows before restore completed", newRows)
	}

	if err := lockTx.Rollback(); err != nil {
		t.Fatalf("release maintenance row lock: %v", err)
	}
	lockHeld = false
	select {
	case result := <-restoreDone:
		if result.code != http.StatusOK {
			t.Fatalf("restore response = %d: %s", result.code, result.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("restore did not finish after maintenance row lock was released")
	}
	select {
	case result := <-uploadDone:
		if result.code < 200 || result.code >= 300 {
			t.Fatalf("gated media upload response = %d: %s", result.code, result.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("gated media upload did not resume after restore completed")
	}
	if got, err := os.ReadFile(originalFile); err != nil || string(got) != string(originalPayload) {
		t.Errorf("restored original attachment bytes = %q, err %v", got, err)
	}
	var storedPath string
	if err := h.db.Raw(`SELECT path FROM media_files WHERE filename = ? AND deleted_at IS NULL`, newName).Scan(&storedPath).Error; err != nil || storedPath == "" {
		t.Fatalf("read post-restore upload path %q: %v", storedPath, err)
	}
	newFile := filepath.Join("data", "uploads", filepath.FromSlash(storedPath))
	if got, err := os.ReadFile(newFile); err != nil || string(got) != string(newPayload) {
		t.Errorf("post-restore upload bytes = %q, err %v", got, err)
	}
}

func waitForRuntimeCoordinationActivity(t *testing.T, db *gorm.DB, timeout time.Duration, predicate string) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int64
		query := `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND ` + predicate
		if err := db.Raw(query).Scan(&count).Error; err != nil {
			t.Fatalf("inspect PostgreSQL activity while waiting for %s: %v", strings.ReplaceAll(predicate, "'", ""), err)
		}
		if count > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestRuntimeCoordinationCorruptQueueJournalKeepsPostgresJobsQueued(t *testing.T) {
	h := newAccessHarness(t)
	const kind = "runtime.corrupt-queue-journal.probe"
	jobQueue, err := queue.Open(queue.Config{Driver: queue.DriverPostgres})
	if err != nil {
		t.Fatalf("open PostgreSQL source queue: %v", err)
	}
	jobID, err := jobQueue.Enqueue(context.Background(), queue.Job{
		Queue: queue.QueueWebhook, Kind: kind, DedupeKey: kind, Payload: json.RawMessage(`{"probe":true}`),
	})
	_ = jobQueue.Close()
	if err != nil || jobID == "" {
		t.Fatalf("seed queued PostgreSQL source job = %q, %v", jobID, err)
	}
	if _, err := settings.SealBytes(queueSwitchJournalKey, []byte("valid journal key fixture")); err != nil {
		t.Fatalf("initialize journal sealing key: %v", err)
	}
	corrupt := core.Option{Key: queueSwitchJournalKey, Value: base64.StdEncoding.EncodeToString([]byte("not valid sealed journal data"))}
	if err := h.db.Create(&corrupt).Error; err != nil {
		t.Fatalf("seed corrupt encrypted queue switch journal: %v", err)
	}
	effects := make(chan queue.Job, 1)
	RegisterSystemJobHandler(kind, func(_ context.Context, _ *gorm.DB, job queue.Job) error {
		effects <- job
		return nil
	})
	SetSystemDatabase(h.db)
	stop := StartSystemServices(context.Background())
	t.Cleanup(func() {
		stop()
		SetSystemDatabase(nil)
	})

	type jobState struct {
		State    string `gorm:"column:state"`
		Attempts int    `gorm:"column:attempts"`
	}
	deadline := time.Now().Add(2 * time.Second)
	var state jobState
	observedEffect := false
	for time.Now().Before(deadline) {
		if err := h.db.Raw(`SELECT state, attempts FROM jobs WHERE id::text = ?`, jobID).Scan(&state).Error; err != nil {
			t.Fatalf("read source job while service starts: %v", err)
		}
		select {
		case <-effects:
			observedEffect = true
		default:
		}
		if observedEffect || state.State != "queued" || state.Attempts != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	SetSystemDatabase(nil)
	if err := h.db.Raw(`SELECT state, attempts FROM jobs WHERE id::text = ?`, jobID).Scan(&state).Error; err != nil {
		t.Fatalf("read source job after stopping services: %v", err)
	}
	if observedEffect {
		t.Error("system services consumed a source job although the encrypted queue recovery journal was corrupt")
	}
	if state.State != "queued" || state.Attempts != 0 {
		t.Errorf("source job after startup with corrupt queue journal = state %q attempts %d; want queued/0", state.State, state.Attempts)
	}
	select {
	case job := <-effects:
		t.Errorf("corrupt journal startup ran probe job %s (%s)", job.ID, job.Kind)
	default:
	}
}

func TestRuntimeCoordinationProductionCannotSwitchQueueToMemory(t *testing.T) {
	h := newAccessHarness(t)
	t.Setenv("JELLY_TEST_POSTGRES", "")
	admin := h.user("admin", "runtime-production-memory-queue-admin")
	token := h.token(admin)
	const kind = "runtime.production-memory-switch.probe"
	if err := h.db.Exec(`INSERT INTO jobs (queue, kind, payload) VALUES ('webhook', ?, '{}'::jsonb)`, kind).Error; err != nil {
		t.Fatalf("seed authoritative PostgreSQL job: %v", err)
	}
	type queueSettings struct {
		Driver  string `gorm:"column:driver"`
		URL     string `gorm:"column:url"`
		Workers string `gorm:"column:workers"`
	}
	readOptions := func() queueSettings {
		var got queueSettings
		if err := h.db.Raw(`SELECT
			COALESCE((SELECT value FROM options WHERE key = 'queue_driver'), 'postgres') AS driver,
			COALESCE((SELECT value FROM options WHERE key = 'queue_url_enc'), '') AS url,
			COALESCE((SELECT value FROM options WHERE key = 'queue_workers'), '') AS workers`).Scan(&got).Error; err != nil {
			t.Fatalf("read queue settings: %v", err)
		}
		return got
	}
	beforeOptions := readOptions()
	if beforeOptions.Driver != queue.DriverPostgres {
		t.Fatalf("fixture source driver = %q, want PostgreSQL", beforeOptions.Driver)
	}
	type jobSnapshot struct {
		State    string `gorm:"column:state"`
		Attempts int    `gorm:"column:attempts"`
	}
	var beforeJob jobSnapshot
	if err := h.db.Raw(`SELECT state, attempts FROM jobs WHERE kind = ?`, kind).Scan(&beforeJob).Error; err != nil {
		t.Fatalf("read source job before production switch: %v", err)
	}
	if beforeJob.State != "queued" || beforeJob.Attempts != 0 {
		t.Fatalf("source job precondition = state %q attempts %d", beforeJob.State, beforeJob.Attempts)
	}

	result := h.request(http.MethodPost, "/api/admin/infra/queue/switch", `{"to":"memory"}`, token, "application/json", "application/json")
	if result.code != http.StatusBadRequest && result.code != http.StatusUnprocessableEntity {
		t.Fatalf("production queue switch to memory = %d: %s; want 400 or 422", result.code, result.body)
	}
	var afterJob jobSnapshot
	if err := h.db.Raw(`SELECT state, attempts FROM jobs WHERE kind = ?`, kind).Scan(&afterJob).Error; err != nil {
		t.Fatalf("read source job after rejected production switch: %v", err)
	}
	if afterJob != beforeJob {
		t.Errorf("rejected production switch changed PostgreSQL source job from %+v to %+v", beforeJob, afterJob)
	}
	if afterOptions := readOptions(); afterOptions != beforeOptions {
		t.Errorf("rejected production switch changed queue settings from %+v to %+v", beforeOptions, afterOptions)
	}
}
