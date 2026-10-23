package data

import (
	"embed"
	"fmt"
	"io/fs"

	"gorm.io/gorm"
)

//go:embed migrations/0001_oauth_identities.sql
var oauthIdentityMigrationFiles embed.FS

func migrateOAuthIdentities(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	migration, err := fs.ReadFile(oauthIdentityMigrationFiles, "migrations/0001_oauth_identities.sql")
	if err != nil {
		return fmt.Errorf("read OAuth identity migration: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(731984201, 1)").Error; err != nil {
			return fmt.Errorf("lock OAuth identity migration: %w", err)
		}
		if err := tx.Exec(string(migration)).Error; err != nil {
			return fmt.Errorf("apply OAuth identity migration: %w", err)
		}
		return nil
	})
}
