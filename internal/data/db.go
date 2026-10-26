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
	// Refuse a database from a newer release before AutoMigrate or the OAuth
	// migration can make any changes to it.
	if err := checkSchemaNotNewer(db); err != nil {
		return err
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
		&core.Widget{},
		&core.WidgetVersion{},
	); err != nil {
		return err
	}

	if err := migrateOAuthIdentities(db); err != nil {
		return err
	}
	if err := runEmbeddedMigrations(db); err != nil {
		return err
	}
	if err := recordSchemaMigrations(db); err != nil {
		return err
	}
	if err := reconcileLegacyProductVariants(db); err != nil {
		return err
	}
	return reconcileLegacyPostSources(db)
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
// in ascending order, grouped by numeric prefix. Each unapplied group runs in
// its own transaction under pg_advisory_xact_lock(731984201, N), N being the
// group's number, so concurrent processes serialise per migration. Applied
// groups are skipped; legacy product data reconciliation runs separately.
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

	// Files with the same numeric prefix are one migration group. Keep the
	// stable filename order within that group and commit all of its SQL together.
	groups := make(map[int][]string)
	var numbers []int
	for _, name := range names {
		number, err := embeddedMigrationNumber(name)
		if err != nil {
			return err
		}
		if number < firstEmbeddedMigration {
			continue
		}
		if _, ok := groups[number]; !ok {
			numbers = append(numbers, number)
		}
		groups[number] = append(groups[number], name)
	}
	sort.Ints(numbers)

	for _, number := range numbers {
		applied, err := schemaVersionApplied(db, number)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		migrations := make(map[string][]byte, len(groups[number]))
		for _, name := range groups[number] {
			migration, err := fs.ReadFile(embeddedMigrationFiles, path.Join("migrations", name))
			if err != nil {
				return fmt.Errorf("read migration %s: %w", name, err)
			}
			migrations[name] = migration
		}

		err = db.Transaction(func(tx *gorm.DB) error {
			// Check before and after taking the xact lock. This avoids waiting on
			// already committed versions and handles another process winning the
			// migration race while we wait.
			applied, err := schemaVersionApplied(tx, number)
			if err != nil || applied {
				return err
			}
			lock := fmt.Sprintf("SELECT pg_advisory_xact_lock(%d, %d)", embeddedMigrationLockClass, number)
			if err := tx.Exec(lock).Error; err != nil {
				return fmt.Errorf("lock migration group %d: %w", number, err)
			}
			applied, err = schemaVersionApplied(tx, number)
			if err != nil || applied {
				return err
			}
			for _, name := range groups[number] {
				if err := tx.Exec(string(migrations[name])).Error; err != nil {
					return fmt.Errorf("apply migration %s: %w", name, err)
				}
			}
			return recordMigrationGroup(tx, number, groups[number])
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// recordMigrationGroup writes its version only after every file sharing that
// number has succeeded in the same transaction. Migration 0016 creates the
// marker table, so it also backfills legacy versions before recording 0016.
func recordMigrationGroup(tx *gorm.DB, number int, names []string) error {
	var exists bool
	if err := tx.Raw("SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists).Error; err != nil {
		return fmt.Errorf("check schema migration marker: %w", err)
	}
	if !exists {
		return nil
	}
	if number >= 16 {
		for version := 1; version <= schemaBackfillThrough; version++ {
			if err := tx.Exec(
				"INSERT INTO schema_migrations (version, name) VALUES (?, ?) ON CONFLICT (version) DO NOTHING",
				version, fmt.Sprintf("%04d_backfilled", version),
			).Error; err != nil {
				return fmt.Errorf("backfill migration %d: %w", version, err)
			}
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("migration group %d has no files", number)
	}
	if err := tx.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES (?, ?) ON CONFLICT (version) DO NOTHING",
		number, names[0],
	).Error; err != nil {
		return fmt.Errorf("record migration group %d: %w", number, err)
	}
	return nil
}

func schemaVersionApplied(db *gorm.DB, version int) (bool, error) {
	var exists bool
	if err := db.Raw("SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("check schema migration marker: %w", err)
	}
	if !exists {
		return false, nil
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version = ?", version).Scan(&count).Error; err != nil {
		return false, fmt.Errorf("read migration marker %d: %w", version, err)
	}
	return count > 0, nil
}

// reconcileLegacyProductVariants keeps the legacy product fields represented
// by a default variant, including products created after migration 0009 ran.
// Products with any existing variant are left unchanged. The separate lock
// avoids rerunning migration DDL or changing the schema-version contract.
func reconcileLegacyProductVariants(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(731984201, 10009)").Error; err != nil {
			return fmt.Errorf("lock legacy product variant reconciliation: %w", err)
		}
		if err := tx.Exec(`
			INSERT INTO product_variants (product_id, name, sku, price, compare_at_price, stock, position, status, created_at, updated_at)
			SELECT p.id,
			       '',
			       COALESCE(p.sku, ''),
			       GREATEST(COALESCE(p.price, 0), 0),
			       GREATEST(COALESCE(p.compare_at_price, 0), 0),
			       GREATEST(COALESCE(p.stock, 0), 0),
			       0,
			       CASE WHEN COALESCE(p.status, 'draft') = 'active' THEN 'active' ELSE 'draft' END,
			       now(),
			       now()
			FROM products p
			WHERE NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id)
		`).Error; err != nil {
			return fmt.Errorf("reconcile legacy product variants: %w", err)
		}
		return nil
	})
}

// reconcileLegacyPostSources preserves the source fields introduced by
// migration 0015 for rows written after that migration ran. Existing non-NULL
// source values are never changed; the separate lock avoids rerunning DDL.
func reconcileLegacyPostSources(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(731984201, 10015)").Error; err != nil {
			return fmt.Errorf("lock legacy post source reconciliation: %w", err)
		}
		updates := []struct {
			name string
			sql  string
		}{
			{"posts markdown source", `UPDATE posts
				SET source = meta->>'markdown', format = 'md'
				WHERE source IS NULL AND meta->>'markdown' IS NOT NULL`},
			{"posts legacy format", `UPDATE posts SET format = 'html' WHERE format IS NULL`},
			{"posts render version", `UPDATE posts SET render_version = 0 WHERE render_version IS NULL`},
			{"posts render trust", `UPDATE posts SET render_trust = 'strict' WHERE render_trust IS NULL`},
			{"revisions markdown source", `UPDATE revisions
				SET source = meta->>'markdown', format = 'md'
				WHERE source IS NULL AND meta->>'markdown' IS NOT NULL`},
			{"revisions legacy format", `UPDATE revisions SET format = 'html' WHERE format IS NULL`},
		}
		for _, update := range updates {
			if err := tx.Exec(update.sql).Error; err != nil {
				return fmt.Errorf("reconcile %s: %w", update.name, err)
			}
		}
		return nil
	})
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
