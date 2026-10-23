package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
	); err != nil {
		return err
	}

	return migrateOAuthIdentities(db)
}

// Migrate runs auto-migration for core models on the global database handle.
func Migrate() error {
	return MigrateDB(DB)
}
