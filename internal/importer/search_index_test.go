package importer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

func TestSearchIndexRewriteLinksRefreshesFoldedContent(t *testing.T) {
	db := importerSearchTestDB(t)
	author := core.User{Username: "import-search-author", Email: "import-search-author@example.test", Role: "admin", Provider: "local"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	post := core.Post{Title: "Imported Link Search", Slug: "imported-link-search", Content: `<p>https://wordpress.example.test/old-story/ import-search-stable</p>`, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	assertFolded := func(term string, want bool) {
		t.Helper()
		var count int64
		if err := db.Table("posts").Where("id = ? AND strpos(search_content, ?) > 0", post.ID, term).Count(&count).Error; err != nil {
			t.Fatalf("query folded content for %q: %v", term, err)
		}
		if got := count == 1; got != want {
			t.Errorf("folded content contains %q = %t, want %t", term, got, want)
		}
	}
	assertFolded("wordpress.example.test", true)
	r := newRunner(db, JobRow{ID: 9, SiteURL: "https://wordpress.example.test"}, Host{}, false)
	r.posts = []uint{post.ID}
	r.links[linkKey("/old-story/")] = "/new-story/"
	if err := r.rewriteLinks(context.Background()); err != nil {
		t.Fatalf("rewrite imported links: %v", err)
	}
	assertFolded("wordpress.example.test", false)
	assertFolded("/new-story/", true)
	assertFolded("import-search-stable", true)
	var stored core.Post
	if err := db.First(&stored, post.ID).Error; err != nil || stored.Content != `<p>/new-story/ import-search-stable</p>` {
		t.Fatalf("rewritten content=%q err=%v", stored.Content, err)
	}
}

func importerSearchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to use the dedicated PostgreSQL container")
	}
	adminCfg := config.DatabaseConfig{Host: "127.0.0.1", Port: "55432", User: "jelly_test", Password: "jelly-local-acceptance", Name: "jelly_acceptance", Timezone: "UTC"}
	admin, err := data.Open(adminCfg)
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	var version int
	if err := admin.Raw("SELECT current_setting('server_version_num')::int").Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("jelly_import_search_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name)).Error; err != nil {
		t.Fatal(err)
	}
	var db *gorm.DB
	t.Cleanup(func() {
		if db != nil {
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		_ = admin.Exec(fmt.Sprintf(`ALTER DATABASE "%s" CONNECTION LIMIT 0`, name)).Error
		_ = admin.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()", name).Error
		drop := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, name)
		if version >= 130000 {
			drop += " WITH (FORCE)"
		}
		if err := admin.Exec(drop).Error; err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	db, err = data.Open(config.DatabaseConfig{Host: adminCfg.Host, Port: adminCfg.Port, User: adminCfg.User, Password: adminCfg.Password, Name: name, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	previousDB, previousConfig := data.DB, config.GlobalConfig
	data.DB, config.GlobalConfig = db, &config.Config{Database: config.DatabaseConfig{Host: adminCfg.Host, Port: adminCfg.Port, User: adminCfg.User, Password: adminCfg.Password, Name: name, Timezone: "UTC"}}
	t.Cleanup(func() {
		data.DB, config.GlobalConfig = previousDB, previousConfig
	})
	return db
}
