package data

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// schemaBackfillThrough is the highest migration number that predates the
// schema_migrations table. The runner records 0001-0015 as applied the first
// time it sees the table; the files for numbers that do not exist here
// (0002-0015 in this tree) are recorded as "backfilled".
const schemaBackfillThrough = 15

// schemaRecordLockNumber is the second advisory-lock key used while recording
// versions. Migration files use numbers from 1 upwards, so 0 never collides.
const schemaRecordLockNumber = 0

// checkSchemaNotNewer refuses to start when the database has applied a
// migration this binary does not know about.
func checkSchemaNotNewer(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	var exists bool
	if err := db.Raw("SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists).Error; err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}
	if !exists {
		return nil
	}

	var newest sql.NullInt64
	if err := db.Raw("SELECT MAX(version) FROM schema_migrations").Scan(&newest).Error; err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	migrations, err := listEmbeddedMigrations()
	if err != nil {
		return err
	}
	known := 0
	if len(migrations) > 0 {
		known = migrations[len(migrations)-1].number
	}
	if newest.Valid && int(newest.Int64) > known {
		return fmt.Errorf("database schema version %d is newer than this binary (%d); refusing to start", newest.Int64, known)
	}
	return nil
}

// recordSchemaMigrations writes one schema_migrations row per embedded file
// and backfills 0001-0015. Rows that already exist are left alone, so the
// function is idempotent and safe to run on every start.
func recordSchemaMigrations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	migrations, err := listEmbeddedMigrations()
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		lock := fmt.Sprintf("SELECT pg_advisory_xact_lock(%d, %d)", embeddedMigrationLockClass, schemaRecordLockNumber)
		if err := tx.Exec(lock).Error; err != nil {
			return fmt.Errorf("lock schema_migrations: %w", err)
		}
		for _, m := range migrations {
			if err := tx.Exec(
				"INSERT INTO schema_migrations (version, name) VALUES (?, ?) ON CONFLICT (version) DO NOTHING",
				m.number, m.name,
			).Error; err != nil {
				return fmt.Errorf("record migration %s: %w", m.name, err)
			}
		}
		for v := 1; v <= schemaBackfillThrough; v++ {
			if err := tx.Exec(
				"INSERT INTO schema_migrations (version, name) VALUES (?, ?) ON CONFLICT (version) DO NOTHING",
				v, fmt.Sprintf("%04d_backfilled", v),
			).Error; err != nil {
				return fmt.Errorf("backfill migration %d: %w", v, err)
			}
		}
		return nil
	})
}

// embeddedMigration is one migration file shipped with the binary.
type embeddedMigration struct {
	number int
	name   string
}

// listEmbeddedMigrations returns the embedded migration files sorted by number.
func listEmbeddedMigrations() ([]embeddedMigration, error) {
	entries, err := embeddedMigrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var out []embeddedMigration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		number, err := embeddedMigrationNumber(entry.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, embeddedMigration{number: number, name: entry.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out, nil
}
