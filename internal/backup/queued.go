package backup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

const (
	stateQueued           = "queued"
	queuedSecretPurpose   = "backup-queued-passphrase-v1"
	backupLockClass       = 731984203
	queuedManifestVersion = 1
)

type queuedManifest struct {
	Version int             `json:"-"`
	Pending *queuedMetadata `json:"-"`
}

type queuedMetadata = pendingMetadata

func (m queuedManifest) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"_jelly_pending": m.Pending})
}

func (m *queuedManifest) UnmarshalJSON(raw []byte) error {
	var envelope struct {
		Version int             `json:"version"`
		Pending *queuedMetadata `json:"_jelly_pending"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	m.Version, m.Pending = envelope.Version, envelope.Pending
	if m.Version == 0 {
		m.Version = queuedManifestVersion
	}
	return nil
}

// PrepareQueued records the real backup row before its queue job is added.
// Passphrases are encrypted with the settings key and only ciphertext is
// stored in the existing manifest column.
func PrepareQueued(ctx context.Context, req Request) (int64, error) {
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if err := checkRequest(req); err != nil {
		return 0, err
	}
	contents, err := normalizeContents(req.Contents)
	if err != nil {
		return 0, err
	}
	kind := req.Kind
	if kind == "" {
		kind = KindManual
	}
	if kind != KindManual && kind != KindScheduled {
		return 0, ErrInvalidKind
	}
	metadata := queuedMetadata{Queued: true}
	if req.Passphrase != "" {
		sealed, err := settings.SealBytes(queuedSecretPurpose, []byte(req.Passphrase))
		if err != nil {
			return 0, fmt.Errorf("backup: seal queued passphrase: %w", err)
		}
		metadata.PassphraseEnc = sealed
	}
	manifest, err := json.Marshal(queuedManifest{Version: queuedManifestVersion, Pending: &metadata})
	if err != nil {
		return 0, err
	}
	rawContents, err := json.Marshal(contents)
	if err != nil {
		return 0, err
	}
	row := record{
		State: stateQueued, Kind: kind, Contents: rawContents, Encrypted: true,
		Note: req.Note, Manifest: manifest, CreatedAt: time.Now().UTC(),
	}
	if req.Actor != 0 {
		actor := req.Actor
		row.CreatedBy = &actor
	}
	if err := createBackupRecord(ctx, &row); err != nil {
		return 0, err
	}
	return row.ID, nil
}

// RunPrepared claims and executes a previously queued backup. A session
// advisory lock makes recovery safe across processes: a running row is
// reclaimed only after no worker still owns its lock. A completed row is an
// idempotent success.
func RunPrepared(ctx context.Context, id int64) (int64, error) {
	if id <= 0 {
		return 0, ErrNotFound
	}
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if !runMu.TryLock() {
		return 0, ErrBusy
	}
	defer runMu.Unlock()
	conn, unlock, err := acquirePreparedLock(ctx, id)
	if err != nil {
		return 0, err
	}
	defer unlock(conn)

	row, err := loadRecord(ctx, id)
	if err != nil {
		return 0, err
	}
	if row.State == stateDone {
		return id, nil
	}
	if row.State != stateQueued && row.State != stateRunning && row.State != stateFailed {
		return 0, ErrNotDone
	}
	var contents []string
	if err := json.Unmarshal(row.Contents, &contents); err != nil {
		return 0, fmt.Errorf("backup: queued contents are damaged: %w", err)
	}
	var metadata queuedManifest
	if len(row.Manifest) > 0 {
		if err := json.Unmarshal(row.Manifest, &metadata); err != nil {
			return 0, fmt.Errorf("backup: queued metadata is damaged: %w", err)
		}
	}
	if metadata.Pending == nil {
		return 0, errors.New("backup: queued metadata is missing")
	}
	passphrase := ""
	if len(metadata.Pending.PassphraseEnc) > 0 {
		plain, err := settings.OpenBytes(queuedSecretPurpose, metadata.Pending.PassphraseEnc)
		if err != nil {
			return 0, errors.New("backup: queued passphrase cannot be opened")
		}
		passphrase = string(plain)
	}
	req := Request{Contents: contents, Note: row.Note, Passphrase: passphrase, Kind: row.Kind}
	if row.CreatedBy != nil {
		req.Actor = *row.CreatedBy
	}
	claim := data.DB.WithContext(ctx).Model(&record{}).Where("id = ? AND state IN ?", id, []string{stateQueued, stateRunning, stateFailed}).Updates(map[string]any{
		"state": stateRunning, "last_error": nil, "finished_at": nil,
	})
	if claim.Error != nil {
		return 0, claim.Error
	}
	if claim.RowsAffected == 0 {
		latest, err := loadRecord(ctx, id)
		if err == nil && latest.State == stateDone {
			return id, nil
		}
		return 0, ErrBusy
	}
	if err := execute(ctx, id, req); err != nil {
		return id, err
	}
	return id, nil
}

// PrepareRetry moves a failed backup back to the durable queue while retaining
// its passphrase only as encrypted pending metadata.
func PrepareRetry(ctx context.Context, id int64, passphrase string, actor uint) error {
	if !runMu.TryLock() {
		return ErrBusy
	}
	defer runMu.Unlock()
	if data.DB == nil {
		return ErrNotInstalled
	}
	if id <= 0 {
		return ErrNotFound
	}
	conn, unlock, err := acquirePreparedLock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock(conn)
	if err := checkRequest(Request{Passphrase: passphrase}); err != nil {
		return err
	}
	row, err := loadRecord(ctx, id)
	if err != nil {
		return err
	}
	if row.State != stateFailed {
		return ErrNotFailed
	}
	metadata := queuedMetadata{Queued: true}
	if passphrase != "" {
		metadata.PassphraseEnc, err = settings.SealBytes(queuedSecretPurpose, []byte(passphrase))
		if err != nil {
			return fmt.Errorf("backup: seal retry passphrase: %w", err)
		}
	}
	stored := decodeDatabaseManifest(row.Manifest)
	if stored.pending == nil {
		stored.pending = &metadata
	} else {
		stored.pending.PassphraseEnc = metadata.PassphraseEnc
	}
	manifest, err := stored.marshal()
	if err != nil {
		return err
	}
	result := data.DB.WithContext(ctx).Model(&record{}).Where("id = ? AND state = ?", id, stateFailed).Updates(map[string]any{
		"state": stateQueued, "manifest": manifest, "last_error": nil, "finished_at": nil,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrBusy
	}
	return nil
}

// QueuedIDs lists pending rows so service startup can recover the narrow
// window between row creation and durable queue acceptance.
func QueuedIDs(ctx context.Context, limit int) ([]int64, error) {
	if data.DB == nil {
		return nil, ErrNotInstalled
	}
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	var ids []int64
	err := data.DB.WithContext(ctx).Model(&record{}).Where("state = ?", stateQueued).
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error
	return ids, err
}

func acquirePreparedLock(ctx context.Context, id int64) (*sql.Conn, func(*sql.Conn), error) {
	sqlDB, err := data.DB.DB()
	if err != nil {
		return nil, nil, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	// A one-key advisory lock is namespaced by the backup ID. The high bits
	// keep this separate from the scheduler's and migration runner's locks.
	key := (int64(backupLockClass) << 32) | (id & 0xffffffff)
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, nil, ErrBusy
	}
	unlock := func(conn *sql.Conn) {
		if conn == nil {
			return
		}
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&released); err != nil || !released {
			// Closing the session releases its lock even if explicit unlock fails.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}
	return conn, unlock, nil
}

// PrepareScheduledIfDue creates one queued scheduled backup for the site's
// configured local day and time. The scheduled row itself prevents duplicate
// daily runs across restarts.
func PrepareScheduledIfDue(ctx context.Context, now time.Time) (int64, error) {
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if settings.Get(ctx, "backup_enabled") != "true" {
		return 0, nil
	}
	loc := time.UTC
	if name := settings.Get(ctx, "site_timezone"); name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	if local.Format("15:04") != settings.Get(ctx, "backup_time") {
		return 0, nil
	}
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
	var started int64
	if err := data.DB.WithContext(ctx).Model(&record{}).
		Where("kind = ? AND created_at >= ?", KindScheduled, dayStart).
		Count(&started).Error; err != nil {
		return 0, err
	}
	if started > 0 {
		return 0, nil
	}
	return PrepareQueued(ctx, Request{Contents: []string{ContentDatabase, ContentSettings}, Kind: KindScheduled})
}
