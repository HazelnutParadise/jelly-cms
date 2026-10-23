package data

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// connectTimeoutSeconds bounds how long opening a candidate connection may
// block, including the initial TCP and TLS handshake.
const connectTimeoutSeconds = 5

// DB is the global database instance.
var DB *gorm.DB

// Open builds an independent database handle for cfg. It never reads or
// replaces DB, so callers can test a connection without disturbing the handle
// that is in use. The caller owns the returned handle and must close it when it
// is no longer needed. On failure Open releases any resources it created.
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := buildDSN(cfg)
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("build database config: %w", redactSecrets(err, dsn, cfg.Password, quoteDSN(cfg.Password)))
	}

	var options []stdlib.OptionOpenDB
	if cfg.Timezone != "" {
		location, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", cfg.Timezone, err)
		}

		connConfig.RuntimeParams["timezone"] = cfg.Timezone
		options = append(options, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
			conn.TypeMap().RegisterType(&pgtype.Type{
				Name:  "timestamp",
				OID:   pgtype.TimestampOID,
				Codec: &pgtype.TimestampCodec{ScanLocation: location},
			})
			return nil
		}))
	}

	sqlDB := stdlib.OpenDB(*connConfig, options...)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		closeDB(sqlDB)
		return nil, fmt.Errorf("failed to connect to database: %w", redactSecrets(err, dsn, cfg.Password, quoteDSN(cfg.Password)))
	}

	return db, nil
}

// Connect opens a candidate connection and only replaces the global handle
// after it succeeds. A failed attempt leaves DB untouched and releases the
// candidate resources.
func Connect(cfg config.DatabaseConfig) error {
	db, err := Open(cfg)
	if err != nil {
		return err
	}

	DB = db
	log.Println("Database connected successfully")
	return nil
}

// buildDSN encodes the connection settings in PostgreSQL keyword/value form.
// quoteDSN escapes each value, so passwords with spaces, quotes, or backslashes
// cannot break out of their field. The timezone is applied through the parsed
// pgx config instead of the DSN.
func buildDSN(cfg config.DatabaseConfig) string {
	settings := []string{
		"host=" + quoteDSN(cfg.Host),
		"port=" + quoteDSN(cfg.Port),
		"user=" + quoteDSN(cfg.User),
		"password=" + quoteDSN(cfg.Password),
		"dbname=" + quoteDSN(cfg.Name),
		"sslmode=disable",
		"connect_timeout=" + strconv.Itoa(connectTimeoutSeconds),
	}
	return strings.Join(settings, " ")
}

// quoteDSN wraps a value in single quotes and escapes backslashes and quotes
// the way the PostgreSQL connection string parser expects.
func quoteDSN(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value)
	return "'" + escaped + "'"
}

// redactSecrets removes connection details that must not reach logs, callers,
// or HTTP responses.
func redactSecrets(err error, secrets ...string) error {
	if err == nil {
		return nil
	}

	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return errors.New(message)
}

// closeDB releases a connection pool that belongs to a failed candidate.
func closeDB(db *sql.DB) {
	if db != nil {
		_ = db.Close()
	}
}

// MigrateDB runs auto-migration for core models on the given handle.
func MigrateDB(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	if err := db.AutoMigrate(
		&core.User{},
		&core.Post{},
		&core.Option{},
		&core.Product{},
		&core.Order{},
		&core.OrderItem{},
		&core.ThemeSettings{},
		&core.RevokedLogin{},
		&core.Category{},
		&core.Tag{},
		&core.Comment{},
		&core.Menu{},
		&core.MenuItem{},
		&core.Revision{},
	); err != nil {
		return err
	}

	if err := migrateOAuthIdentities(db); err != nil {
		return err
	}
	return runEmbeddedMigrations(db)
}

// embeddedMigrationFiles holds the SQL migrations shipped with the binary.
//
//go:embed migrations/*.sql
var embeddedMigrationFiles embed.FS

// embeddedMigrationLockClass is the first key of pg_advisory_xact_lock for the
// embedded migrations; the second key is the migration number. The OAuth
// identity migration uses (731984201, 1), so the numbers never collide.
const embeddedMigrationLockClass = 731984201

// firstEmbeddedMigration is the lowest migration number this runner applies.
// 0001 belongs to migrateOAuthIdentities, which runs before this runner.
const firstEmbeddedMigration = 2

// runEmbeddedMigrations applies every embedded migration numbered 0002 and up,
// in ascending order. Each file runs in its own transaction under
// pg_advisory_xact_lock(731984201, N), N being the file's number, so
// concurrent processes serialise per migration. Every file is written to be
// idempotent, so running the whole set again on a migrated database is a no-op.
func runEmbeddedMigrations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	entries, err := fs.ReadDir(embeddedMigrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		number, err := embeddedMigrationNumber(name)
		if err != nil {
			return err
		}
		if number < firstEmbeddedMigration {
			continue
		}

		migration, err := fs.ReadFile(embeddedMigrationFiles, path.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		err = db.Transaction(func(tx *gorm.DB) error {
			lock := fmt.Sprintf("SELECT pg_advisory_xact_lock(%d, %d)", embeddedMigrationLockClass, number)
			if err := tx.Exec(lock).Error; err != nil {
				return fmt.Errorf("lock migration %s: %w", name, err)
			}
			if err := tx.Exec(string(migration)).Error; err != nil {
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// embeddedMigrationNumber parses the numeric prefix of a file such as
// "0002_user_account_fields.sql".
func embeddedMigrationNumber(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found {
		return 0, fmt.Errorf("migration %q has no number prefix", name)
	}
	number, err := strconv.Atoi(prefix)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("migration %q has an invalid number", name)
	}
	return number, nil
}

// Migrate runs auto-migration for core models on the global database handle.
func Migrate() error {
	return MigrateDB(DB)
}
