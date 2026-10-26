package main

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
)

func TestUpdateStartupRegressionNewerSchemaRejectsBeforeMutation(t *testing.T) {
	requireBootPostgres(t)
	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	cfg := bootDatabaseConfig(database)
	seed := openBootHandle(t, cfg)
	if err := seed.Exec(`CREATE TABLE schema_migrations (version integer PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.Exec(`INSERT INTO schema_migrations(version,name) VALUES (999,'future.sql')`).Error; err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := seed.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	useBootGlobals(t)
	if err := config.Save(&config.Config{Database: cfg}); err != nil {
		t.Fatalf("save test config: %v", err)
	}
	err := initializeSite()
	if err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("initializeSite error = %v, want newer schema version 999", err)
	}
	if data.DB != nil {
		t.Fatalf("data.DB published after newer schema rejection: %p", data.DB)
	}
	var after int64
	if err := seed.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("newer-schema rejection created/dropped tables: before=%d after=%d", before, after)
	}
	var version int
	var name string
	if err := seed.Raw(`SELECT version,name FROM schema_migrations`).Row().Scan(&version, &name); err != nil {
		t.Fatal(err)
	}
	if version != 999 || name != "future.sql" {
		t.Errorf("future schema marker changed to %d/%q", version, name)
	}
}

func TestUpdateStartupRegressionBindFailureDoesNotConfirmHealthy(t *testing.T) {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		t.Skipf("cannot reserve server port for bind-failure test: %v", err)
	}
	defer listener.Close()
	t.Chdir(t.TempDir())
	useBootGlobals(t)
	if err := update.SaveState(update.StatePath, update.State{Current: "old", Latest: "new", Status: update.StatusApplied}); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "address already in use") {
		t.Fatalf("run() with occupied :8080 = %v, want bind failure", err)
	}
	st, err := update.LoadState(update.StatePath)
	if err != nil {
		t.Fatalf("load update state after failed bind: %v", err)
	}
	if st.Status != update.StatusApplied {
		t.Errorf("update status after HTTP bind failure = %q, want still applied pending healthy startup", st.Status)
	}
	if _, err := os.Stat(update.StatePath); err != nil {
		t.Errorf("update state disappeared after bind failure: %v", err)
	}
}
