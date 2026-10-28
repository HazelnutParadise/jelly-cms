package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
)

func TestStartupThemeRestoresSavedThemeWithoutWriting(t *testing.T) {
	requireBootPostgres(t)
	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	db := openBootHandle(t, bootDatabaseConfig(database))
	if err := data.MigrateDB(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&core.Option{Key: "active_theme", Value: "selected"}).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"default", "selected"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"`+name+`"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tm := theme.NewManager(root)
	if err := activateStartupTheme(context.Background(), tm, db); err != nil {
		t.Fatal(err)
	}
	if tm.GetActive() != "selected" {
		t.Fatalf("active=%q, want selected", tm.GetActive())
	}
	var saved core.Option
	if err := db.Where("key = ?", "active_theme").First(&saved).Error; err != nil || saved.Value != "selected" {
		t.Fatalf("saved option=%+v err=%v", saved, err)
	}
}

func TestStartupThemeFallsBackAndPreservesRecord(t *testing.T) {
	requireBootPostgres(t)
	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	db := openBootHandle(t, bootDatabaseConfig(database))
	if err := data.MigrateDB(db); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, config := range map[string]string{"default": `{"name":"default"}`, "broken": "{", "cyclic": `{"name":"cyclic"}`} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(config), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cyclicAssets := filepath.Join(root, "cyclic", "assets")
	if err := os.MkdirAll(cyclicAssets, 0755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{"a.css": `@import "b.css";`, "b.css": `@import "a.css";`} {
		if err := os.WriteFile(filepath.Join(cyclicAssets, file), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"missing", "broken", "../escape", "cyclic"} {
		t.Run(value, func(t *testing.T) {
			if err := db.Create(&core.Option{Key: "active_theme", Value: value}).Error; err != nil {
				t.Fatal(err)
			}
			tm := theme.NewManager(root)
			err := activateStartupTheme(context.Background(), tm, db)
			if err == nil {
				t.Fatal("want fallback diagnostic")
			}
			if tm.GetActive() != "default" {
				t.Fatalf("active=%q", tm.GetActive())
			}
			var got core.Option
			if e := db.Where("key = ?", "active_theme").First(&got).Error; e != nil || got.Value != value {
				t.Fatalf("saved=%+v err=%v", got, e)
			}
			if e := db.Delete(&core.Option{}, "key = ?", "active_theme").Error; e != nil {
				t.Fatal(e)
			}
		})
	}
	if err := db.Create(&core.Option{Key: "active_theme", Value: ""}).Error; err != nil {
		t.Fatal(err)
	}
	if err := activateStartupTheme(context.Background(), theme.NewManager(root), db); err != nil {
		t.Fatalf("blank option should use default: %v", err)
	}
	blankManager := theme.NewManager(root)
	if err := activateStartupTheme(context.Background(), blankManager, db); err != nil || blankManager.GetActive() != "default" {
		t.Fatalf("blank option fallback active=%q err=%v", blankManager.GetActive(), err)
	}
	var blank core.Option
	if err := db.First(&blank, "key = ?", "active_theme").Error; err != nil || blank.Value != "" {
		t.Fatalf("blank fallback changed stored option: %+v err=%v", blank, err)
	}
	if err := db.Model(&core.Option{}).Where("key = ?", "active_theme").Delete(&core.Option{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := activateStartupTheme(context.Background(), theme.NewManager(root), db); err != nil {
		t.Fatalf("missing option fallback: %v", err)
	}
	missingManager := theme.NewManager(root)
	if err := activateStartupTheme(context.Background(), missingManager, db); err != nil || missingManager.GetActive() != "default" {
		t.Fatalf("missing option fallback active=%q err=%v", missingManager.GetActive(), err)
	}
	defaultBroken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(defaultBroken, "default"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultBroken, "default", "theme.json"), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&core.Option{Key: "active_theme", Value: "broken"}).Error; err != nil {
		t.Fatal(err)
	}
	bothManager := theme.NewManager(defaultBroken)
	if err := activateStartupTheme(context.Background(), bothManager, db); err == nil {
		t.Fatal("invalid selected and default themes unexpectedly succeeded")
	}
	var preserved core.Option
	if err := db.First(&preserved, "key = ?", "active_theme").Error; err != nil || preserved.Value != "broken" {
		t.Fatalf("double failure changed stored selection: %+v err=%v", preserved, err)
	}
	closed := db
	sqlDB, err := closed.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	closedManager := theme.NewManager(root)
	if err := activateStartupTheme(context.Background(), closedManager, closed); err == nil || closedManager.GetActive() != "default" {
		t.Fatalf("closed DB should report read error and activate default, active=%q err=%v", closedManager.GetActive(), err)
	}
	if err := activateStartupTheme(context.Background(), theme.NewManager(defaultBroken), nil); err == nil {
		t.Fatal("missing default theme unexpectedly succeeded")
	}
}

func TestStartupThemeNilDBUsesDefault(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "default")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"default"}`), 0644); err != nil {
		t.Fatal(err)
	}
	tm := theme.NewManager(root)
	if err := activateStartupTheme(context.Background(), tm, nil); err != nil {
		t.Fatal(err)
	}
	if tm.GetActive() != "default" {
		t.Fatalf("active=%q", tm.GetActive())
	}
}
