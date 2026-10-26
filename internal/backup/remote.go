package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StorageResolver resolves a private backup destination by its fixed policy ID.
// It is configured once by server setup and may be safely read while workers run.
type StorageResolver func(context.Context, int64) (storage.Driver, error)

var resolverState struct {
	sync.RWMutex
	fn StorageResolver
}

// ConfigureStorageResolver installs the storage bridge used for remote backup
// destinations. Passing nil disables remote destinations.
func ConfigureStorageResolver(fn StorageResolver) {
	resolverState.Lock()
	resolverState.fn = fn
	resolverState.Unlock()
}

func resolveStorage(ctx context.Context, policyID int64) (storage.Driver, error) {
	resolverState.RLock()
	fn := resolverState.fn
	resolverState.RUnlock()
	if fn == nil {
		return nil, errors.New("backup: remote storage is not configured")
	}
	return fn(ctx, policyID)
}

type remoteMetadata struct {
	PolicyID int64  `json:"policy_id"`
	Key      string `json:"key"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type pendingMetadata struct {
	Queued        bool   `json:"queued,omitempty"`
	PassphraseEnc []byte `json:"passphrase_enc,omitempty"`
	PolicyID      int64  `json:"policy_id,omitempty"`
	TransferKey   string `json:"transfer_key,omitempty"`
	TransferSHA   string `json:"transfer_sha256,omitempty"`
	TransferSize  int64  `json:"transfer_size,omitempty"`
}

type databaseManifest struct {
	remote    *remoteMetadata
	pending   *pendingMetadata
	fields    map[string]json.RawMessage
	malformed bool
}

func decodeDatabaseManifest(raw []byte) databaseManifest {
	m := databaseManifest{fields: make(map[string]json.RawMessage)}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m.fields); err != nil || m.fields == nil {
			m.malformed = true
			m.fields = make(map[string]json.RawMessage)
		}
	}
	if v := m.fields["_jelly_remote"]; len(v) != 0 {
		_ = json.Unmarshal(v, &m.remote)
	}
	if v := m.fields["_jelly_pending"]; len(v) != 0 {
		_ = json.Unmarshal(v, &m.pending)
	}
	return m
}

func (m databaseManifest) marshal() ([]byte, error) {
	if m.fields == nil {
		m.fields = make(map[string]json.RawMessage)
	}
	if m.remote == nil {
		delete(m.fields, "_jelly_remote")
	} else {
		b, err := json.Marshal(m.remote)
		if err != nil {
			return nil, err
		}
		m.fields["_jelly_remote"] = b
	}
	if m.pending == nil || (!m.pending.Queued && len(m.pending.PassphraseEnc) == 0 && m.pending.PolicyID == 0 && m.pending.TransferKey == "") {
		delete(m.fields, "_jelly_pending")
	} else {
		b, err := json.Marshal(m.pending)
		if err != nil {
			return nil, err
		}
		m.fields["_jelly_pending"] = b
	}
	return json.Marshal(m.fields)
}

func persistDatabaseManifest(ctx context.Context, id int64, m databaseManifest, updates map[string]any) error {
	raw, err := m.marshal()
	if err != nil {
		return err
	}
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["manifest"] = string(raw)
	result := data.DB.WithContext(ctx).Model(&record{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func transferKey(id int64, sum string) string {
	return fmt.Sprintf("backups/%d/%s.jbk", id, sum)
}

func putAndVerify(ctx context.Context, drv storage.Driver, key, filename string, wantSize int64, wantSHA string) error {
	if wantSize <= 0 || wantSize > MaxArchiveBytes || !validSHA256(wantSHA) {
		return ErrInvalidArchive
	}
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("backup: open local archive for upload: %w", err)
	}
	if err := drv.Put(ctx, key, f, wantSize, "application/octet-stream"); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: upload archive: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup: close uploaded archive: %w", err)
	}
	r, err := drv.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("backup: read back uploaded archive: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, wantSize+1))
	closeErr := r.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("backup: verify uploaded archive: %w", errors.Join(err, closeErr))
	}
	if n > MaxArchiveBytes || n != wantSize || hex.EncodeToString(h.Sum(nil)) != wantSHA {
		return ErrChecksum
	}
	return nil
}

func deleteRemoteArchive(ctx context.Context, row record) error {
	m := decodeDatabaseManifest(row.Manifest)
	if m.malformed {
		return errors.New("backup: refusing to delete backup with malformed manifest metadata")
	}
	var policyID int64
	var key string
	if _, present := m.fields["_jelly_remote"]; present {
		meta, ok, err := parseVerifiedRemoteMetadata(row)
		if err != nil {
			return fmt.Errorf("backup: refusing to delete backup with invalid remote metadata: %w", err)
		}
		if !ok {
			return errors.New("backup: refusing to delete backup with incomplete remote metadata")
		}
		policyID, key = meta.PolicyID, meta.Key
	} else if pending := m.pending; pending != nil && (pending.PolicyID > 0 || pending.TransferKey != "" || pending.TransferSHA != "" || pending.TransferSize != 0) {
		if pending.PolicyID <= 0 {
			return errors.New("backup: refusing to delete backup with incomplete pending remote metadata")
		}
		if pending.TransferKey == "" {
			return nil
		}
		if !validSHA256(pending.TransferSHA) || pending.TransferSize <= 0 || pending.TransferSize > MaxArchiveBytes || pending.TransferSize != row.SizeBytes || row.SHA256 == nil || pending.TransferSHA != *row.SHA256 {
			return errors.New("backup: refusing to delete backup with invalid pending remote metadata")
		}
		if _, err := storage.CleanPath(pending.TransferKey); err != nil {
			return errors.New("backup: refusing to delete backup with invalid pending remote key")
		}
		policyID, key = pending.PolicyID, pending.TransferKey
	} else {
		return nil
	}
	if _, err := storage.CleanPath(key); err != nil {
		return ErrInvalidArchive
	}
	drv, err := resolveStorage(ctx, policyID)
	if err != nil {
		return fmt.Errorf("backup: resolve remote destination: %w", err)
	}
	if err := drv.Delete(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Missing objects are idempotently deleted by the storage contract.
		return fmt.Errorf("backup: delete remote archive: %w", err)
	}
	return nil
}

func verifiedRemoteMetadata(row record) (remoteMetadata, bool) {
	meta, ok, err := parseVerifiedRemoteMetadata(row)
	return meta, ok && err == nil
}

func parseVerifiedRemoteMetadata(row record) (remoteMetadata, bool, error) {
	meta := decodeDatabaseManifest(row.Manifest).remote
	if meta == nil {
		return remoteMetadata{}, false, nil
	}
	if meta.PolicyID <= 0 || meta.Size <= 0 || meta.Size > MaxArchiveBytes || row.SizeBytes != meta.Size ||
		row.SHA256 == nil || !validSHA256(meta.SHA256) || meta.SHA256 != *row.SHA256 {
		return remoteMetadata{}, false, ErrChecksum
	}
	if _, err := storage.CleanPath(meta.Key); err != nil {
		return remoteMetadata{}, false, ErrInvalidArchive
	}
	return *meta, true, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func pruneLocalCopy(ctx context.Context, row record) error {
	conn, unlock, err := acquirePreparedLock(ctx, row.ID)
	if err != nil {
		return err
	}
	defer unlock(conn)
	current, err := loadRecord(ctx, row.ID)
	if err != nil {
		return err
	}
	if current.State != stateDone {
		return ErrBusy
	}
	if _, ok := verifiedRemoteMetadata(current); !ok {
		return errors.New("backup: refusing to prune unverified local-only archive")
	}
	return removeArchive(current)
}

func fetchPendingArchive(ctx context.Context, row record, pending *pendingMetadata) (string, error) {
	if pending == nil || pending.PolicyID <= 0 || pending.TransferKey == "" || !validSHA256(pending.TransferSHA) || pending.TransferSize <= 0 || pending.TransferSize > MaxArchiveBytes {
		return "", ErrChecksum
	}
	if row.SHA256 == nil || row.SizeBytes != pending.TransferSize || *row.SHA256 != pending.TransferSHA {
		return "", ErrChecksum
	}
	if _, err := storage.CleanPath(pending.TransferKey); err != nil {
		return "", ErrInvalidArchive
	}
	cacheDir := filepath.Join(Directory, ".private-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return "", err
	}
	filename := filepath.Join(cacheDir, fmt.Sprintf("%d-%s.jbk", row.ID, pending.TransferSHA))
	if row.SHA256 != nil && row.SizeBytes == pending.TransferSize && *row.SHA256 == pending.TransferSHA && localArchiveMatches(row, filename) {
		return filename, nil
	}
	drv, err := resolveStorage(ctx, pending.PolicyID)
	if err != nil {
		return "", fmt.Errorf("backup: resolve pending destination: %w", err)
	}
	r, err := drv.Open(ctx, pending.TransferKey)
	if err != nil {
		return "", fmt.Errorf("backup: open pending archive: %w", err)
	}
	tmp, err := os.CreateTemp(cacheDir, ".retry-fetch-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, pending.TransferSize+1))
	readCloseErr := r.Close()
	if copyErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: fetch pending archive: %w", errors.Join(copyErr, readCloseErr))
	}
	if readCloseErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: close pending archive: %w", readCloseErr)
	}
	if n > MaxArchiveBytes || n != pending.TransferSize || hex.EncodeToString(h.Sum(nil)) != pending.TransferSHA {
		_ = tmp.Close()
		return "", ErrChecksum
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return "", err
	}
	return filename, nil
}

// PolicyReferenced reports whether any backup row still relies on a storage
// policy. Call it inside the caller's transaction after locking the policy row
// FOR UPDATE to prevent create/update/delete races.
func PolicyReferenced(ctx context.Context, tx *gorm.DB, policyID int64) (bool, error) {
	if tx == nil || policyID <= 0 {
		return false, errors.New("backup: invalid policy reference check")
	}
	var rows []record
	err := tx.WithContext(ctx).Where("jsonb_exists(manifest, '_jelly_remote') OR jsonb_exists(manifest, '_jelly_pending')").Find(&rows).Error
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		m := decodeDatabaseManifest(row.Manifest)
		if m.malformed {
			return true, nil
		}
		if _, present := m.fields["_jelly_remote"]; present {
			remote, valid, err := parseVerifiedRemoteMetadata(row)
			if err != nil || !valid {
				return true, nil
			}
			if remote.PolicyID == policyID {
				return true, nil
			}
		}
		if _, present := m.fields["_jelly_pending"]; present {
			pending := m.pending
			if pending == nil || pending.PolicyID < 0 {
				return true, nil
			}
			if pending.TransferKey != "" || pending.TransferSHA != "" || pending.TransferSize != 0 {
				if pending.PolicyID <= 0 || pending.TransferKey == "" || !validSHA256(pending.TransferSHA) ||
					pending.TransferSize <= 0 || pending.TransferSize > MaxArchiveBytes || pending.TransferSize != row.SizeBytes ||
					row.SHA256 == nil || pending.TransferSHA != *row.SHA256 {
					return true, nil
				}
				if _, err := storage.CleanPath(pending.TransferKey); err != nil {
					return true, nil
				}
			}
			if pending.PolicyID == policyID {
				return true, nil
			}
		}
	}
	return false, nil
}

func archiveLocalPath(row record) (string, error) {
	if row.FilePath == nil {
		return "", os.ErrNotExist
	}
	return archiveFile(*row.FilePath)
}

func localArchiveMatches(row record, filename string) bool {
	if row.SHA256 == nil || row.SizeBytes <= 0 {
		return false
	}
	info, err := os.Stat(filename)
	if err != nil || info.Size() != row.SizeBytes || info.Size() > MaxArchiveBytes {
		return false
	}
	sz, sum, err := fileDigest(filename)
	return err == nil && sz == row.SizeBytes && sum == *row.SHA256
}

func ensureLocalArchive(ctx context.Context, row record) (string, func(), error) {
	conn, unlock, err := acquirePreparedLock(ctx, row.ID)
	if err != nil {
		return "", nil, err
	}
	released := false
	release := func() {
		if !released {
			released = true
			unlock(conn)
		}
	}
	row, err = loadRecord(ctx, row.ID)
	if err != nil {
		release()
		return "", nil, err
	}
	local, err := archiveLocalPath(row)
	if err == nil && localArchiveMatches(row, local) {
		return local, release, nil
	}
	meta, ok := verifiedRemoteMetadata(row)
	if !ok {
		release()
		if err == nil {
			return "", nil, ErrChecksum
		}
		return "", nil, err
	}
	if _, err := storage.CleanPath(meta.Key); err != nil {
		release()
		return "", nil, ErrInvalidArchive
	}
	drv, err := resolveStorage(ctx, meta.PolicyID)
	if err != nil {
		release()
		return "", nil, fmt.Errorf("backup: resolve remote destination: %w", err)
	}
	r, err := drv.Open(ctx, meta.Key)
	if err != nil {
		release()
		return "", nil, fmt.Errorf("backup: open remote archive: %w", err)
	}
	if err := prepareDir(Directory); err != nil {
		_ = r.Close()
		release()
		return "", nil, err
	}
	cacheDir := filepath.Join(Directory, ".private-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		_ = r.Close()
		release()
		return "", nil, fmt.Errorf("backup: prepare private archive cache: %w", err)
	}
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		_ = r.Close()
		release()
		return "", nil, fmt.Errorf("backup: secure private archive cache: %w", err)
	}
	final := filepath.Join(cacheDir, fmt.Sprintf("%d-%s.jbk", row.ID, meta.SHA256))
	if localArchiveMatches(row, final) {
		_ = r.Close()
		return final, release, nil
	}
	tmp, err := os.CreateTemp(cacheDir, ".fetch-*")
	if err != nil {
		_ = r.Close()
		release()
		return "", nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, meta.Size+1))
	readCloseErr := r.Close()
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || readCloseErr != nil {
		release()
		return "", nil, errors.Join(copyErr, closeErr, readCloseErr)
	}
	if n > MaxArchiveBytes || n != meta.Size || hex.EncodeToString(h.Sum(nil)) != meta.SHA256 {
		release()
		return "", nil, ErrChecksum
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		release()
		return "", nil, err
	}
	if err := os.Rename(tmpName, final); err != nil {
		release()
		return "", nil, err
	}
	return final, release, nil
}

func stableDownloadPath(row record, source string) (string, error) {
	if row.SHA256 == nil || row.SizeBytes <= 0 || row.SizeBytes > MaxArchiveBytes || !validSHA256(*row.SHA256) {
		return "", ErrChecksum
	}
	cacheDir := filepath.Join(Directory, ".private-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return "", err
	}
	filename := filepath.Join(cacheDir, fmt.Sprintf("%d-%s.jbk", row.ID, *row.SHA256))
	if filepath.Clean(source) == filepath.Clean(filename) || localArchiveMatches(row, filename) {
		return filename, nil
	}
	if _, err := os.Stat(filename); err == nil {
		if err := os.Remove(filename); err != nil {
			return "", err
		}
	}
	if err := os.Link(source, filename); err == nil {
		if err := os.Chmod(filename, 0o600); err != nil {
			_ = os.Remove(filename)
			return "", err
		}
		return filename, nil
	} else if !errors.Is(err, os.ErrExist) {
		in, openErr := os.Open(source)
		if openErr != nil {
			return "", openErr
		}
		defer func() { _ = in.Close() }()
		tmp, createErr := os.CreateTemp(cacheDir, ".download-copy-*")
		if createErr != nil {
			return "", createErr
		}
		tmpName := tmp.Name()
		defer func() { _ = os.Remove(tmpName) }()
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(in, row.SizeBytes+1))
		if copyErr != nil || n != row.SizeBytes || hex.EncodeToString(h.Sum(nil)) != *row.SHA256 {
			_ = tmp.Close()
			return "", errors.Join(copyErr, ErrChecksum)
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return "", err
		}
		if err := tmp.Close(); err != nil {
			return "", err
		}
		if err := os.Rename(tmpName, filename); err != nil {
			return "", err
		}
		return filename, nil
	}
	if err := os.Chmod(filename, 0o600); err != nil {
		return "", err
	}
	return filename, nil
}

// createBackupRecord captures the selected destination and validates its
// purpose while holding a shared policy-row lock in the same transaction that
// inserts the backup row. Destination edits/deletes must take a conflicting
// lock and check this reference before committing.
func createBackupRecord(ctx context.Context, row *record) error {
	return data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updateMetadata, preUpdate := beforeUpdateMetadataFromContext(ctx)
		if preUpdate && row.Kind != KindManual {
			return errors.New("backup: pre-update metadata requires a manual backup")
		}
		policyID := int64(0)
		if row.Kind != KindPreRestore && !preUpdate {
			var option core.Option
			err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("key = ?", "backup_policy_id").Take(&option).Error
			if err == nil {
				var parseErr error
				policyID, parseErr = strconv.ParseInt(option.Value, 10, 64)
				if parseErr != nil || policyID < 0 {
					return errors.New("backup: configured backup destination is invalid")
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("backup: read destination setting: %w", err)
			} else if spec, ok := settings.Lookup("backup_policy_id"); ok && spec.Default != "" {
				id, parseErr := strconv.ParseInt(spec.Default, 10, 64)
				if parseErr != nil || id < 0 {
					return errors.New("backup: default backup destination is invalid")
				}
				policyID = id
			}
		}
		manifest := decodeDatabaseManifest(row.Manifest)
		if preUpdate {
			metadata, err := json.Marshal(updateMetadata)
			if err != nil {
				return fmt.Errorf("backup: encode pre-update metadata: %w", err)
			}
			manifest.fields["_jelly_update"] = metadata
		}
		if policyID > 0 {
			var policy struct {
				ID        int64
				Purpose   string
				DeletedAt *time.Time
			}
			if err := tx.Table("media_policies").Clauses(clause.Locking{Strength: "SHARE"}).
				Select("id, purpose, deleted_at").Where("id = ?", policyID).Take(&policy).Error; err != nil {
				return fmt.Errorf("backup: configured destination is unavailable: %w", err)
			}
			if policy.Purpose != "backup" || policy.DeletedAt != nil {
				return errors.New("backup: configured destination is not an active backup policy")
			}
			if manifest.pending == nil {
				manifest.pending = &pendingMetadata{}
			}
			manifest.pending.PolicyID = policyID
		}
		raw, err := manifest.marshal()
		if err != nil {
			return err
		}
		row.Manifest = raw
		if err := tx.Create(row).Error; err != nil {
			return fmt.Errorf("backup: create row: %w", err)
		}
		return nil
	})
}
