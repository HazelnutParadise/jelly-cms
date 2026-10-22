package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"gorm.io/gorm"
)

// initializeSite recovers an interrupted installation before touching the
// configured database. It leaves the site uninstalled when no configuration
// exists, and never clears a loaded configuration because of a connection
// failure.
func initializeSite() error {
	if err := install.RecoverInstallation(); err != nil {
		return fmt.Errorf("recover the installation: %w", err)
	}

	// Recovery publishes a verified connection for a committed installation.
	// Reuse it instead of opening a second one.
	if data.DB != nil {
		if config.GlobalConfig == nil {
			if _, err := config.Load(); err != nil {
				return fmt.Errorf("load the configuration: %w", err)
			}
		}
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load the configuration: %w", err)
	}

	candidate, err := data.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to the database: %w", err)
	}

	// Publish the candidate only after its schema is ready. A failed migration
	// must leave data.DB nil so startup does not serve an unusable site.
	if err := data.MigrateDB(candidate); err != nil {
		closeCandidate(candidate)
		return fmt.Errorf("migrate the database: %w", err)
	}

	data.DB = candidate
	return nil
}

// closeCandidate releases a connection pool that will not be published.
func closeCandidate(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return
	}
	_ = sqlDB.Close()
}
