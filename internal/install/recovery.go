package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

// pendingInstallation is the journal written before the installation
// transaction commits. It only contains the database configuration and the
// random marker that is stored in the same transaction; the administrator
// password is never written to disk in clear text.
type pendingInstallation struct {
	Database       config.DatabaseConfig `json:"database"`
	InstallationID string                `json:"installation_id"`
}

// RecoverInstallation reconciles a journal left behind by an interrupted
// installation. It only completes an installation that is proven committed by
// the installation marker in the database; it never reruns installation.
func RecoverInstallation() (err error) {
	installMutex.Lock()
	defer installMutex.Unlock()

	// Without a data directory there cannot be a journal. Avoid creating one
	// just to perform a no-op recovery.
	if info, statErr := os.Stat("data"); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
	} else if !info.IsDir() {
		return nil
	}

	release, err := acquireDataDirLock()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	pending, found, err := readPendingInstallation()
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	existing, hasConfig, err := currentConfig()
	if err != nil {
		return err
	}
	if hasConfig && existing.Database != pending.Database {
		return fmt.Errorf("pending installation belongs to a different database than the existing configuration")
	}

	// A database that cannot be read leaves the journal untouched so a later
	// attempt can decide with real information instead of guessing.
	db, err := data.Open(pending.Database)
	if err != nil {
		return fmt.Errorf("open the database of the pending installation: %w", err)
	}
	published := false
	defer func() {
		if !published {
			closeDatabase(db)
		}
	}()

	// Inspect the database inside a transaction that waits for the same
	// advisory lock the installation transaction takes. The installation
	// creates its tables and marker in one transaction, so once the lock is
	// available the inspection sees either the whole committed installation or
	// nothing at all, never an in-flight one whose marker is not visible yet.
	var marker string
	var markerFound bool
	var installedData bool
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", advisoryLockKey).Error; err != nil {
			return fmt.Errorf("lock the database of the pending installation: %w", err)
		}

		usersExist, err := tableExists(tx, "users")
		if err != nil {
			return err
		}
		optionsExist, err := tableExists(tx, "options")
		if err != nil {
			return err
		}

		if usersExist {
			userCount, err := countRows(tx, &core.User{})
			if err != nil {
				return err
			}
			installedData = userCount > 0
		}
		if optionsExist {
			marker, markerFound, err = readOption(tx, installationIDKey)
			if err != nil {
				return err
			}
			optionCount, err := countRows(tx, &core.Option{})
			if err != nil {
				return err
			}
			if optionCount > 0 {
				installedData = true
			}
		}
		return nil
	})
	if err != nil {
		// A database that cannot be read leaves the journal untouched so a
		// later attempt can decide with real information instead of guessing.
		return err
	}

	if !markerFound {
		// A complete installation always carries the marker. A missing users
		// or options table, or empty ones, proves that no installation data was
		// committed: the schema is created inside the same transaction and is
		// rolled back with it. Clearing the journal does not migrate, create
		// tables, or touch anything that belongs to an existing site.
		if !installedData {
			return removePendingInstallation()
		}
		return fmt.Errorf("the database already contains a site but no installation marker; refusing to recover")
	}

	if marker != pending.InstallationID {
		return fmt.Errorf("the installation marker in the database does not match the pending installation")
	}

	// The transaction committed. Verify the existing configuration instead of
	// overwriting a configuration that does not belong to this journal.
	if hasConfig {
		if config.GlobalConfig == nil {
			config.GlobalConfig = existing
		}
		if data.DB == nil {
			data.DB = db
			published = true
		}
		return removePendingInstallation()
	}

	cfg := &config.Config{Database: pending.Database}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save the recovered installation configuration: %w", err)
	}

	config.GlobalConfig = cfg
	data.DB = db
	published = true

	return removePendingInstallation()
}

func writePendingInstallation(pending pendingInstallation) error {
	payload, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the pending installation journal: %w", err)
	}

	dir := filepath.Dir(pendingPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create the installation journal directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".install-pending-*")
	if err != nil {
		return fmt.Errorf("create the pending installation journal: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set pending installation journal permissions: %w", err)
	}
	written, err := tmp.Write(payload)
	if err == nil && written != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the pending installation journal: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync the pending installation journal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the pending installation journal: %w", err)
	}
	if err := os.Rename(tmpName, pendingPath); err != nil {
		return fmt.Errorf("replace the pending installation journal: %w", err)
	}
	renamed = true
	if err := os.Chmod(pendingPath, 0600); err != nil {
		_ = os.Remove(pendingPath)
		return fmt.Errorf("set pending installation journal permissions: %w", err)
	}
	return nil
}

func readPendingInstallation() (pendingInstallation, bool, error) {
	payload, err := os.ReadFile(pendingPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pendingInstallation{}, false, nil
		}
		return pendingInstallation{}, false, fmt.Errorf("read the pending installation journal: %w", err)
	}

	var pending pendingInstallation
	if err := json.Unmarshal(payload, &pending); err != nil {
		return pendingInstallation{}, false, fmt.Errorf("decode the pending installation journal: %w", err)
	}
	if pending.InstallationID == "" {
		return pendingInstallation{}, false, fmt.Errorf("the pending installation journal has no installation id")
	}
	if pending.Database.Host == "" || pending.Database.Port == "" || pending.Database.User == "" || pending.Database.Name == "" {
		return pendingInstallation{}, false, fmt.Errorf("the pending installation journal has an incomplete database configuration")
	}
	return pending, true, nil
}

func removePendingInstallation() error {
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the pending installation journal: %w", err)
	}
	return nil
}

func currentConfig() (*config.Config, bool, error) {
	if config.GlobalConfig != nil {
		return config.GlobalConfig, true, nil
	}

	payload, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read the existing configuration: %w", err)
	}

	cfg := &config.Config{}
	if err := json.Unmarshal(payload, cfg); err != nil {
		return nil, false, fmt.Errorf("decode the existing configuration: %w", err)
	}
	return cfg, true, nil
}

func readOption(db *gorm.DB, key string) (string, bool, error) {
	var options []core.Option
	if err := db.Where("key = ?", key).Limit(1).Find(&options).Error; err != nil {
		return "", false, fmt.Errorf("read option %q: %w", key, err)
	}
	if len(options) == 0 {
		return "", false, nil
	}
	return options[0].Value, true, nil
}

// tableExists reports whether the table is visible through the connection
// search path. Errors are returned instead of being treated as a missing table,
// because a database that cannot be inspected must never discard the journal.
func tableExists(db *gorm.DB, table string) (bool, error) {
	var exists bool
	if err := db.Raw("SELECT to_regclass(?) IS NOT NULL", table).Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("check whether table %q exists: %w", table, err)
	}
	return exists, nil
}

func countRows(db *gorm.DB, model any) (int64, error) {
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count %T rows: %w", model, err)
	}
	return count, nil
}
