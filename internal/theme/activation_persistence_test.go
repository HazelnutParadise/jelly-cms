package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

func persistenceDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("requires dedicated PostgreSQL test database")
	}
	admin, err := data.Open(config.DatabaseConfig{Host: "127.0.0.1", Port: "55432", User: "jelly_test", Password: "jelly-local-acceptance", Name: "jelly_acceptance", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a, _ := admin.DB()
		if a != nil {
			_ = a.Close()
		}
	})
	name := fmt.Sprintf("jelly_theme_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err = admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, name)).Error; err != nil {
			t.Errorf("drop dedicated theme test database %s: %v", name, err)
		}
	})
	db, err := data.Open(config.DatabaseConfig{Host: "127.0.0.1", Port: "55432", User: "jelly_test", Password: "jelly-local-acceptance", Name: name, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if err = data.MigrateDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func persistenceTheme(t *testing.T, root, name, marker string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"`+name+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.html"), []byte(`{{define "layout"}}{{template "content" .}}{{end}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`{{define "content"}}`+marker+`{{end}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "site.css"), []byte(marker), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestThemeActivationPersistSuccessAndInvalidAtomicity(t *testing.T) {
	db := persistenceDB(t)
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	persistenceTheme(t, root, "b", "B")
	m := NewManager(root)
	for _, name := range []string{"a", "b", "a"} {
		if err := m.ActivateAndPersist(name, db); err != nil {
			t.Fatalf("persist %s: %v", name, err)
		}
		var opt core.Option
		if err := db.First(&opt, "key = ?", "active_theme").Error; err != nil || opt.Value != name {
			t.Fatalf("saved=%+v err=%v", opt, err)
		}
		if m.GetActive() != name {
			t.Fatalf("runtime=%q want %q", m.GetActive(), name)
		}
	}
	before := m.GetActive()
	if err := m.ActivateAndPersist("missing", db); err == nil {
		t.Fatal("missing theme unexpectedly activated")
	}
	if m.GetActive() != before {
		t.Fatalf("invalid activation changed runtime to %q", m.GetActive())
	}
	var opt core.Option
	if err := db.First(&opt, "key = ?", "active_theme").Error; err != nil || opt.Value != before {
		t.Fatalf("invalid activation changed DB: %+v err=%v", opt, err)
	}
}

func TestThemeActivationPersistConcurrentDatabaseMatchesRuntime(t *testing.T) {
	db := persistenceDB(t)
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	persistenceTheme(t, root, "b", "B")
	m := NewManager(root)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		name := "a"
		if i%2 == 1 {
			name = "b"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.ActivateAndPersist(name, db); err != nil {
				t.Errorf("ActivateAndPersist(%s): %v", name, err)
			}
		}()
	}
	wg.Wait()
	var opt core.Option
	if err := db.First(&opt, "key = ?", "active_theme").Error; err != nil {
		t.Fatal(err)
	}
	if opt.Value != m.GetActive() {
		t.Fatalf("database=%q runtime=%q", opt.Value, m.GetActive())
	}
}

func TestThemeActivationPersistDatabaseFailureKeepsRuntimeAndSnapshot(t *testing.T) {
	db := persistenceDB(t)
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	persistenceTheme(t, root, "b", "B")
	m := NewManager(root)
	if err := m.ActivateAndPersist("a", db); err != nil {
		t.Fatal(err)
	}
	before := m.AssetSnapshot()
	beforeURL := m.AssetURL("/theme/assets/site.css")
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.ActivateAndPersist("b", db); err == nil {
		t.Fatal("closed database persistence unexpectedly succeeded")
	}
	if m.GetActive() != "a" || m.AssetSnapshot() != before || m.AssetURL("/theme/assets/site.css") != beforeURL {
		t.Fatalf("failed persistence changed runtime snapshot: active=%q snapshotSame=%v URL=%q", m.GetActive(), m.AssetSnapshot() == before, m.AssetURL("/theme/assets/site.css"))
	}
	body, err := m.Render("index.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "A" {
		t.Fatalf("render after failed persistence=%q want A", body)
	}
}
