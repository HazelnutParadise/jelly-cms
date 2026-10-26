package server

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

type pluginInstallJournal struct {
	Action          string    `json:"action"`
	ID              string    `json:"id"`
	Version         string    `json:"version"`
	PermissionsHash string    `json:"permissions_hash,omitempty"`
	ManifestSHA256  string    `json:"manifest_sha256"`
	TreeSHA256      string    `json:"tree_sha256"`
	CommitStamp     time.Time `json:"commit_stamp"`
	Stage           string    `json:"stage"`
	Backup          string    `json:"backup"`
	Phase           string    `json:"phase"`
	Journaled       bool      `json:"-"`
}

func validatePluginInstallJournal(j pluginInstallJournal) error {
	if !plugin.ValidPluginID(j.ID) || strings.ContainsAny(j.ID, `/\\`) {
		return fmt.Errorf("invalid plugin transaction id")
	}
	if j.Action != "install" && j.Action != "uninstall" {
		return fmt.Errorf("invalid plugin transaction action")
	}
	if j.Phase != "prepared" && j.Phase != "swapped" {
		return fmt.Errorf("invalid plugin transaction phase")
	}
	validName := func(name, prefix string) bool {
		return name != "" && name != "." && name != ".." && !filepath.IsAbs(name) && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`) && strings.HasPrefix(name, prefix)
	}
	if !validName(j.Backup, ".jelly-plugin-"+j.Action+"-"+j.ID+"-") {
		return fmt.Errorf("invalid plugin transaction backup path")
	}
	if j.Action == "install" {
		if !validName(j.Stage, ".jelly-plugin-stage-") || !storeSHA256Pattern.MatchString(j.ManifestSHA256) || !storeSHA256Pattern.MatchString(j.TreeSHA256) || j.CommitStamp.IsZero() || j.CommitStamp.Nanosecond()%1000 != 0 {
			return fmt.Errorf("invalid plugin install transaction data")
		}
	} else if j.Stage != "" || !storeSHA256Pattern.MatchString(j.TreeSHA256) {
		return fmt.Errorf("invalid plugin uninstall transaction data")
	}
	return nil
}

func validateExistingPluginTransactionPaths(j pluginInstallJournal) error {
	paths := []string{filepath.Join(pluginDirRoot, j.ID), pluginTransactionPath(j.Backup)}
	if j.Stage != "" {
		paths = append(paths, pluginTransactionPath(j.Stage))
	}
	for _, p := range paths {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("plugin transaction path is not a regular directory: %s", p)
		}
	}
	return nil
}

func writePluginInstallJournal(path string, j pluginInstallJournal) error {
	if err := validatePluginInstallJournal(j); err != nil {
		return err
	}
	if err := validateExistingPluginTransactionPaths(j); err != nil {
		return err
	}
	if path != pluginInstallJournalPath(j.ID) {
		return fmt.Errorf("invalid plugin transaction journal path")
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}

func pluginManifestDigest(dir string) (plugin.PluginMetadata, string, error) {
	manifest := filepath.Join(dir, "plugin.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return plugin.PluginMetadata{}, "", err
	}
	meta, err := plugin.ParseManifest(raw)
	if err != nil {
		return plugin.PluginMetadata{}, "", err
	}
	sum := sha256.Sum256(raw)
	return meta, hex.EncodeToString(sum[:]), nil
}

func pluginTreeDigest(root string) (string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin tree contains symlink %s", path)
		}
		if path != root {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		full := filepath.Join(root, rel)
		info, err := os.Lstat(full)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			_, _ = io.WriteString(h, "D\x00"+filepath.ToSlash(rel)+"\x00")
			continue
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("plugin tree contains unsupported entry %s", rel)
		}
		_, _ = io.WriteString(h, "F\x00"+filepath.ToSlash(rel)+"\x00")
		f, err := os.Open(full)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashPluginArchive(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// recoverPluginInstallJournals resolves interrupted disk/DB swaps before the
// runtime decides whether a plugin directory is loadable.
func recoverPluginInstallJournals() error {
	pluginFilesMu.Lock()
	defer pluginFilesMu.Unlock()
	entries, err := os.ReadDir(pluginDirRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".journal" {
			continue
		}
		path := filepath.Join(pluginDirRoot, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read plugin transaction journal %s: %w", path, err)
		}
		var j pluginInstallJournal
		if err := json.Unmarshal(raw, &j); err != nil || validatePluginInstallJournal(j) != nil || path != pluginInstallJournalPath(j.ID) {
			return fmt.Errorf("invalid plugin transaction journal %s", path)
		}
		id := j.ID
		err = withPluginAdvisoryLock(context.Background(), id, func() error {
			fresh, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			var current pluginInstallJournal
			if err := json.Unmarshal(fresh, &current); err != nil || validatePluginInstallJournal(current) != nil || current.ID != id || path != pluginInstallJournalPath(current.ID) {
				return fmt.Errorf("invalid plugin transaction journal %s", path)
			}
			return recoverPluginInstallJournal(path, current)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func recoverPluginInstallJournal(path string, j pluginInstallJournal) error {
	if err := validateExistingPluginTransactionPaths(j); err != nil {
		return fmt.Errorf("invalid plugin transaction paths %s: %w", j.ID, err)
	}
	target := filepath.Join(pluginDirRoot, j.ID)
	var rows []struct {
		Version         string    `gorm:"column:version"`
		PermissionsHash string    `gorm:"column:permissions_granted_hash"`
		UpdatedAt       time.Time `gorm:"column:updated_at"`
	}
	queryErr := fmt.Errorf("database is unavailable")
	if data.DB != nil {
		queryErr = data.DB.Raw(`SELECT version, permissions_granted_hash, updated_at FROM plugins WHERE id = ?`, j.ID).Scan(&rows).Error
	}
	if queryErr != nil {
		return fmt.Errorf("resolve plugin transaction %s: %w", j.ID, queryErr)
	}
	committed := len(rows) == 1 && rows[0].Version == j.Version && rows[0].UpdatedAt.Equal(j.CommitStamp) && (j.PermissionsHash == "" || rows[0].PermissionsHash == j.PermissionsHash)
	if j.Action == "uninstall" {
		committed = len(rows) == 0
	}
	if committed && j.Action != "uninstall" {
		meta, digest, checkErr := pluginManifestDigest(target)
		treeDigest, treeErr := pluginTreeDigest(target)
		committed = checkErr == nil && treeErr == nil && meta.ID == j.ID && meta.Version == j.Version && digest == j.ManifestSHA256 && treeDigest == j.TreeSHA256
	}
	if committed && j.Action == "uninstall" {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("plugin uninstall %s committed but target still exists", j.ID)
		} else if !os.IsNotExist(err) {
			return err
		}
		backup := pluginTransactionPath(j.Backup)
		if _, err := os.Lstat(backup); err == nil {
			digest, digestErr := pluginTreeDigest(backup)
			if digestErr != nil || digest != j.TreeSHA256 {
				return fmt.Errorf("plugin uninstall %s backup does not match journal", j.ID)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if committed {
		if err := os.RemoveAll(pluginTransactionPath(j.Backup)); err != nil {
			return fmt.Errorf("remove committed plugin backup %s: %w", j.ID, err)
		}
		if j.Stage != "" {
			if err := os.RemoveAll(pluginTransactionPath(j.Stage)); err != nil {
				return fmt.Errorf("remove plugin stage %s: %w", j.ID, err)
			}
		}
	} else {
		backup := pluginTransactionPath(j.Backup)
		stageMissing := false
		if j.Stage != "" {
			_, stageErr := os.Stat(pluginTransactionPath(j.Stage))
			stageMissing = os.IsNotExist(stageErr)
			if stageErr != nil && !stageMissing {
				return fmt.Errorf("inspect plugin stage %s: %w", j.ID, stageErr)
			}
		}
		if _, statErr := os.Stat(backup); statErr == nil {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove failed plugin target %s: %w", j.ID, err)
			}
			if err := os.Rename(backup, target); err != nil {
				return fmt.Errorf("restore plugin backup %s: %w", j.ID, err)
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect plugin backup %s: %w", j.ID, statErr)
		} else if j.Action != "uninstall" && (j.Phase == "swapped" || (j.Phase == "prepared" && j.Stage != "" && stageMissing)) {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove uncommitted plugin target %s: %w", j.ID, err)
			}
		} else if j.Action == "uninstall" && j.Phase == "swapped" {
			return fmt.Errorf("plugin uninstall %s has no backup to restore", j.ID)
		}
		if j.Stage != "" {
			if err := os.RemoveAll(pluginTransactionPath(j.Stage)); err != nil {
				return fmt.Errorf("remove plugin stage %s: %w", j.ID, err)
			}
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plugin transaction journal %s: %w", j.ID, err)
	}
	return nil
}

func pluginInstallJournalPath(id string) string {
	return filepath.Join(pluginDirRoot, ".install-"+id+".journal")
}

func pluginTransactionPath(name string) string {
	return filepath.Join(filepath.Dir(pluginDirRoot), name)
}

var errPluginRecoveryPending = errors.New("plugin recovery is pending")

func withPluginInstallLock(ctx context.Context, id string, fn func() error) error {
	if !plugin.ValidPluginID(id) {
		return errPluginNotFound
	}
	if data.DB == nil {
		return fmt.Errorf("plugin database is unavailable")
	}
	pluginFilesMu.Lock()
	defer pluginFilesMu.Unlock()
	return withPluginAdvisoryLock(ctx, id, func() error {
		if _, err := os.Stat(pluginInstallJournalPath(id)); err == nil {
			return errPluginRecoveryPending
		} else if !os.IsNotExist(err) {
			return err
		}
		return fn()
	})
}

func withPluginAdvisoryLock(ctx context.Context, id string, fn func() error) error {
	if !plugin.ValidPluginID(id) {
		return errPluginNotFound
	}
	if data.DB == nil {
		return fmt.Errorf("plugin database is unavailable")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	key := int32(h.Sum32())
	return data.DB.Connection(func(conn *gorm.DB) (callbackErr error) {
		if err := conn.WithContext(ctx).Exec(`SELECT pg_advisory_lock(731984203, ?)`, key).Error; err != nil {
			discardPluginDBConnection(conn)
			return err
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			unlockErr := conn.WithContext(unlockCtx).Exec(`SELECT pg_advisory_unlock(731984203, ?)`, key).Error
			if unlockErr != nil {
				discardPluginDBConnection(conn)
			}
			callbackErr = errors.Join(callbackErr, unlockErr)
		}()
		return fn()
	})
}

func discardPluginDBConnection(conn *gorm.DB) {
	if rawConn, ok := conn.ConnPool.(interface{ Raw(func(any) error) error }); ok {
		_ = rawConn.Raw(func(any) error { return driver.ErrBadConn })
	}
}

func commitPluginArchive(staged *plugin.StagedArchive, ctxVersion, permissionsHash string, stamp time.Time) (pluginInstallJournal, error) {
	meta, digest, err := pluginManifestDigest(staged.Stage)
	if err != nil {
		return pluginInstallJournal{}, err
	}
	if meta.ID != staged.ID || meta.Version != ctxVersion {
		return pluginInstallJournal{}, fmt.Errorf("staged plugin identity changed")
	}
	treeDigest, err := pluginTreeDigest(staged.Stage)
	if err != nil {
		return pluginInstallJournal{}, err
	}
	backupName := fmt.Sprintf(".jelly-plugin-install-%s-%d", staged.ID, time.Now().UnixNano())
	j := pluginInstallJournal{Action: "install", ID: staged.ID, Version: staged.Version, PermissionsHash: permissionsHash, ManifestSHA256: digest, TreeSHA256: treeDigest, CommitStamp: stamp, Stage: filepath.Base(staged.Stage), Backup: backupName, Phase: "prepared"}
	if staged.Target != filepath.Join(pluginDirRoot, staged.ID) || filepath.Base(staged.Stage) != j.Stage {
		return pluginInstallJournal{}, fmt.Errorf("invalid staged plugin transaction paths")
	}
	if err := validatePluginInstallJournal(j); err != nil {
		return pluginInstallJournal{}, err
	}
	if err := validateExistingPluginTransactionPaths(j); err != nil {
		return pluginInstallJournal{}, err
	}
	jp := pluginInstallJournalPath(staged.ID)
	if err := writePluginInstallJournal(jp, j); err != nil {
		return j, err
	}
	j.Journaled = true
	backup := pluginTransactionPath(backupName)
	if _, err := os.Stat(staged.Target); err == nil {
		if err := os.Rename(staged.Target, backup); err != nil {
			_ = os.Remove(jp)
			return j, err
		}
	} else if !os.IsNotExist(err) {
		_ = os.Remove(jp)
		return j, err
	}
	if err := os.Rename(staged.Stage, staged.Target); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			if restoreErr := os.Rename(backup, staged.Target); restoreErr != nil {
				return j, fmt.Errorf("activate plugin: %w; restore prior plugin: %v", err, restoreErr)
			}
			_ = os.Remove(jp)
		} else if !os.IsNotExist(statErr) {
			return j, fmt.Errorf("activate plugin: %w; inspect prior plugin backup: %v", err, statErr)
		} else {
			_ = os.Remove(jp)
		}
		return j, err
	}
	j.Phase = "swapped"
	if err := writePluginInstallJournal(jp, j); err != nil {
		return j, err
	}
	return j, nil
}

func finishPluginArchiveCommit(j pluginInstallJournal) error {
	if !j.Journaled {
		return fmt.Errorf("plugin transaction journal was not persisted")
	}
	if err := validatePluginInstallJournal(j); err != nil {
		return err
	}
	if err := validateExistingPluginTransactionPaths(j); err != nil {
		return err
	}
	if err := os.RemoveAll(pluginTransactionPath(j.Backup)); err != nil {
		return err
	}
	if j.Stage != "" {
		if err := os.RemoveAll(pluginTransactionPath(j.Stage)); err != nil {
			return err
		}
	}
	if err := os.Remove(pluginInstallJournalPath(j.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func rollbackPluginArchive(j pluginInstallJournal) error {
	if !j.Journaled {
		return fmt.Errorf("plugin transaction journal was not persisted")
	}
	if err := validatePluginInstallJournal(j); err != nil {
		return err
	}
	if err := validateExistingPluginTransactionPaths(j); err != nil {
		return err
	}
	target := filepath.Join(pluginDirRoot, j.ID)
	backup := pluginTransactionPath(j.Backup)
	if _, err := os.Stat(backup); err == nil {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.Rename(backup, target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if j.Phase == "swapped" && j.Action != "uninstall" {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	if j.Stage != "" {
		if err := os.RemoveAll(pluginTransactionPath(j.Stage)); err != nil {
			return err
		}
	}
	if err := os.Remove(pluginInstallJournalPath(j.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
