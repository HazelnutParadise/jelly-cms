package data

import (
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// unreachableConfig points at 127.0.0.1:1, where nothing listens, so a connect
// attempt fails immediately with "connection refused" and never reaches a real
// database.
func unreachableConfig() config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "UTC",
	}
}

// newIdleDB builds a database handle that has never opened a socket. It stands
// in for the connection that is already in use, so a failing Connect can be
// checked for replacing it.
func newIdleDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(
		postgres.Open("host=127.0.0.1 user=jelly password=s3cret dbname=jelly port=1 sslmode=disable"),
		&gorm.Config{DisableAutomaticPing: true},
	)
	if err != nil {
		t.Fatalf("build the idle database handle: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get the sql.DB behind the idle handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return db
}

// keepDB restores the global database handle when the test ends.
func keepDB(t *testing.T) {
	t.Helper()

	previous := DB
	t.Cleanup(func() { DB = previous })
}

func TestConnectFailureKeepsThePreviousDatabaseHandle(t *testing.T) {
	keepDB(t)

	existing := newIdleDB(t)
	DB = existing

	err := Connect(unreachableConfig())
	if err == nil {
		t.Fatalf("Connect() = nil, want an error for an unreachable database")
	}
	if !strings.Contains(err.Error(), "failed to connect to database") {
		t.Errorf("Connect() error = %v, want it to report the database connection failure", err)
	}
	if DB != existing {
		t.Errorf("Connect() replaced the database handle that is in use: DB = %p, want the previous handle %p", DB, existing)
	}
}

func TestConnectFailureLeavesNoUsableDatabaseHandle(t *testing.T) {
	keepDB(t)

	DB = nil

	err := Connect(unreachableConfig())
	if err == nil {
		t.Fatalf("Connect() = nil, want an error for an unreachable database")
	}
	if DB != nil {
		t.Errorf("Connect() left a database handle behind after a failed connection: DB = %p, want nil", DB)
	}
}

func TestMigrateWithoutDatabaseReturnsError(t *testing.T) {
	keepDB(t)

	DB = nil

	if err := Migrate(); err == nil {
		t.Errorf("Migrate() = nil, want an error when the database is not initialised")
	}
}
