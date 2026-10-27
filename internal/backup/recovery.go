package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

const restoreRecoveryRoot = "data/.restore-recovery"

type restoreJournal struct {
	Phase              string `json:"phase"`
	ReplaceAttachments bool   `json:"replace_attachments"`
	HadAttachments     bool   `json:"had_attachments"`
	ReplaceSecrets     bool   `json:"replace_secrets"`
	HadSecrets         bool   `json:"had_secrets"`
	MarkerID           string `json:"marker_id"`
	AttachmentsApplied bool   `json:"attachments_applied"`
	SecretsApplied     bool   `json:"secrets_applied"`
	OldAttachmentsHash string `json:"old_attachments_hash,omitempty"`
	NewAttachmentsHash string `json:"new_attachments_hash,omitempty"`
	OldSecretsHash     string `json:"old_secrets_hash,omitempty"`
	NewSecretsHash     string `json:"new_secrets_hash,omitempty"`
}

func prepareRestoreJournal(m Manifest) (string, error) {
	j := restoreJournal{Phase: "applying"}
	for _, content := range m.Contents {
		j.ReplaceAttachments = j.ReplaceAttachments || content == ContentAttachments
	}
	for _, file := range m.Files {
		j.ReplaceSecrets = j.ReplaceSecrets || file.Path == authSecretsEntry
		if file.Path == authSecretsEntry {
			j.NewSecretsHash = file.SHA256
		}
	}
	if j.ReplaceAttachments {
		j.NewAttachmentsHash = manifestAttachmentsHash(m)
	}
	if !j.ReplaceAttachments && !j.ReplaceSecrets {
		return "", nil
	}
	var marker [16]byte
	if _, err := rand.Read(marker[:]); err != nil {
		return "", fmt.Errorf("backup: create restore marker: %w", err)
	}
	j.MarkerID = hex.EncodeToString(marker[:])
	if j.ReplaceAttachments {
		if _, err := os.Stat(attachmentsDir); err == nil {
			j.HadAttachments = true
			j.OldAttachmentsHash, err = attachmentDirectoryHash(attachmentsDir)
			if err != nil {
				return "", fmt.Errorf("backup: fingerprint attachments before restore: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("backup: inspect attachments before restore: %w", err)
		}
	}
	if j.ReplaceSecrets {
		if _, err := os.Stat(authSecretsFile); err == nil {
			j.HadSecrets = true
			_, j.OldSecretsHash, err = fileDigest(authSecretsFile)
			if err != nil {
				return "", fmt.Errorf("backup: fingerprint authentication secrets before restore: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("backup: inspect authentication secrets before restore: %w", err)
		}
	}
	if err := os.MkdirAll(restoreRecoveryRoot, 0o700); err != nil {
		return "", fmt.Errorf("backup: create restore recovery directory: %w", err)
	}
	dir, err := os.MkdirTemp(restoreRecoveryRoot, "restore-*")
	if err != nil {
		return "", fmt.Errorf("backup: create restore journal: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := writeRestoreJournal(dir, j); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := syncDirectory(restoreRecoveryRoot); err != nil {
		return "", fmt.Errorf("backup: sync restore recovery directory: %w", err)
	}
	return dir, nil
}

func writeRestoreJournal(dir string, j restoreJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".journal-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, "journal.json")); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func completeRestoreJournal(dir string) error {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		return err
	}
	var j restoreJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return err
	}
	j.Phase = "committed"
	if err := writeRestoreJournal(dir, j); err != nil {
		return err
	}
	if err := deleteRestoreCommitMarker(data.DB, j.MarkerID); err != nil {
		return fmt.Errorf("remove restore commit marker: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return syncDirectory(restoreRecoveryRoot)
}

func rollbackRestoreJournal(dir string) error {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		return fmt.Errorf("read restore journal: %w", err)
	}
	var j restoreJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("decode restore journal: %w", err)
	}
	if err := rollbackRestoreJournalState(dir, j); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return syncDirectory(restoreRecoveryRoot)
}

func restoreJournalMarkerID(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		return "", err
	}
	var j restoreJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return "", err
	}
	return j.MarkerID, nil
}

func setRestoreResourceApplied(dir, resource string) error {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		return err
	}
	var j restoreJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return err
	}
	switch resource {
	case "attachments":
		j.AttachmentsApplied = true
	case "secrets":
		j.SecretsApplied = true
	default:
		return fmt.Errorf("unknown restore resource %q", resource)
	}
	return writeRestoreJournal(dir, j)
}

func rollbackRestoreJournalState(dir string, j restoreJournal) error {
	if j.ReplaceAttachments {
		old := filepath.Join(dir, "uploads-old")
		if _, err := os.Stat(old); err == nil {
			if err := os.RemoveAll(attachmentsDir); err != nil {
				return fmt.Errorf("remove partial restored attachments: %w", err)
			}
			if err := os.Rename(old, attachmentsDir); err != nil {
				return fmt.Errorf("restore previous attachments: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect previous attachments: %w", err)
		} else {
			_, statErr := os.Stat(attachmentsDir)
			if errors.Is(statErr, fs.ErrNotExist) && !j.HadAttachments {
				// The journal was durable before any swap, and the old path did not
				// exist, so there is nothing to roll back.
			} else if statErr != nil {
				return fmt.Errorf("inspect attachments during recovery: %w", statErr)
			} else if j.HadAttachments {
				got, digestErr := attachmentDirectoryHash(attachmentsDir)
				if digestErr != nil || got != j.OldAttachmentsHash {
					return fmt.Errorf("restore recovery is uncertain: previous attachments do not match their fingerprint: %v", digestErr)
				}
			} else {
				got, digestErr := attachmentDirectoryHash(attachmentsDir)
				if digestErr != nil || got != j.NewAttachmentsHash {
					return fmt.Errorf("restore recovery is uncertain: attachments do not match the expected pre-swap or restored fingerprint: %v", digestErr)
				}
				if err := os.RemoveAll(attachmentsDir); err != nil {
					return fmt.Errorf("remove partial restored attachments: %w", err)
				}
			}
		}
		if err := os.RemoveAll(filepath.Join(filepath.Dir(attachmentsDir), ".uploads-restore-new")); err != nil {
			return fmt.Errorf("clean staged attachments during recovery: %w", err)
		}
	}
	if j.ReplaceSecrets {
		old := filepath.Join(dir, "auth-secrets-old")
		if _, err := os.Stat(old); err == nil {
			if err := os.Remove(authSecretsFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove partial authentication secrets: %w", err)
			}
			if err := os.Rename(old, authSecretsFile); err != nil {
				return fmt.Errorf("restore previous authentication secrets: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect previous authentication secrets: %w", err)
		} else {
			_, statErr := os.Stat(authSecretsFile)
			if errors.Is(statErr, fs.ErrNotExist) && !j.HadSecrets {
				// No original or replacement secret file exists yet.
			} else if statErr != nil {
				return fmt.Errorf("inspect authentication secrets during recovery: %w", statErr)
			} else if j.HadSecrets {
				_, got, digestErr := fileDigest(authSecretsFile)
				if digestErr != nil || got != j.OldSecretsHash {
					return fmt.Errorf("restore recovery is uncertain: previous authentication secrets do not match their fingerprint: %v", digestErr)
				}
			} else {
				_, got, digestErr := fileDigest(authSecretsFile)
				if digestErr != nil || got != j.NewSecretsHash {
					return fmt.Errorf("restore recovery is uncertain: authentication secrets do not match the expected pre-swap or restored fingerprint: %v", digestErr)
				}
				if err := os.Remove(authSecretsFile); err != nil {
					return fmt.Errorf("remove partial authentication secrets: %w", err)
				}
			}
		}
	}
	return syncDirectory("data")
}

func manifestAttachmentsHash(m Manifest) string {
	h := sha256.New()
	for _, file := range m.Files {
		if strings.HasPrefix(file.Path, attachmentsPrefix) {
			path := strings.TrimPrefix(file.Path, attachmentsPrefix)
			_, _ = fmt.Fprintf(h, "%d:", len(path))
			_, _ = h.Write([]byte(path))
			_, _ = h.Write([]byte(file.SHA256))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func attachmentDirectoryHash(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported attachment entry %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		_, sum, err := fileDigest(path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		_, _ = fmt.Fprintf(h, "%d:", len(rel))
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte(sum))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RecoverInterruptedRestore rolls back file swaps left by a process crash.
// It must run before the server begins serving requests.
func RecoverInterruptedRestore() error {
	entries, err := os.ReadDir(restoreRecoveryRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup: read restore recovery directory: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("backup: load configuration to recover restore: %w", err)
	}
	db, err := data.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("backup: connect to database to recover restore: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("backup: open recovery database pool: %w", err)
	}
	defer sqlDB.Close()
	for _, entry := range entries {
		if !entry.IsDir() || !filepath.IsLocal(entry.Name()) {
			continue
		}
		dir := filepath.Join(restoreRecoveryRoot, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
		if err != nil {
			return fmt.Errorf("backup: read interrupted restore journal: %w", err)
		}
		var j restoreJournal
		if err := json.Unmarshal(raw, &j); err != nil {
			return fmt.Errorf("backup: decode interrupted restore journal: %w", err)
		}
		committed, err := restoreCommitMarkerExists(db, j.MarkerID)
		if err != nil {
			return fmt.Errorf("backup: verify restore commit marker: %w", err)
		}
		if j.Phase == "committed" || committed {
			j.Phase = "committed"
			if err := writeRestoreJournal(dir, j); err != nil {
				return fmt.Errorf("backup: persist committed restore journal: %w", err)
			}
			if err := deleteRestoreCommitMarker(db, j.MarkerID); err != nil {
				return fmt.Errorf("backup: remove restore commit marker: %w", err)
			}
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("backup: clean committed restore journal: %w", err)
			}
			if err := syncDirectory(restoreRecoveryRoot); err != nil {
				return fmt.Errorf("backup: sync committed restore cleanup: %w", err)
			}
			continue
		}
		if j.Phase != "applying" {
			return fmt.Errorf("backup: restore journal has unknown phase %q", j.Phase)
		}
		if err := rollbackRestoreJournalState(dir, j); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("backup: clean recovered restore journal: %w", err)
		}
		if err := syncDirectory(restoreRecoveryRoot); err != nil {
			return fmt.Errorf("backup: sync recovered restore cleanup: %w", err)
		}
	}
	return nil
}

func restoreCommitMarkerKey(id string) string { return "__jelly_restore_commit_" + id }

func writeRestoreCommitMarker(tx *gorm.DB, id string) error {
	if id == "" {
		return nil
	}
	if err := tx.Exec(`INSERT INTO options (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, restoreCommitMarkerKey(id), id).Error; err != nil {
		return fmt.Errorf("write restore commit marker: %w", err)
	}
	var got string
	if err := tx.Raw("SELECT value FROM options WHERE key = ?", restoreCommitMarkerKey(id)).Scan(&got).Error; err != nil {
		return fmt.Errorf("read restore commit marker: %w", err)
	}
	if got != id {
		return fmt.Errorf("restore commit marker did not match")
	}
	return nil
}

func restoreCommitMarkerExists(db *gorm.DB, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var got string
	err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Raw("SELECT value FROM options WHERE key = ?", restoreCommitMarkerKey(id)).Scan(&got).Error
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	return got == id, nil
}

func deleteRestoreCommitMarker(db *gorm.DB, id string) error {
	if id == "" || db == nil {
		return nil
	}
	return db.WithContext(context.Background()).Exec("DELETE FROM options WHERE key = ? AND value = ?", restoreCommitMarkerKey(id), id).Error
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
